package downloader

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// CachedWindow is a prefetch window owned by WindowCache.
// Multiple ReadAheadReaders for the same URL share the same CachedWindow.
// Fields URL, Start, Size, done are immutable after creation.
// state, data, err, fetchedAt are written once under WindowCache.mu then immutable.
// lastAccess is updated atomically on every access.
type CachedWindow struct {
	URL       string
	Start     int64
	Size      int64
	data      []byte
	err       error
	done      chan struct{} // closed when fetch completes (data/err set before)
	state     WindowState  // written under wc.mu
	fetchedAt int64        // unix nano, written once under wc.mu
	_         [0]int64     // padding
	lastAccess int64       // unix nano, updated atomically
}

// CacheStats is a JSON-serialisable snapshot of the window cache.
type CacheStats struct {
	DataMB  int64 `json:"data_mb"`
	MaxMB   int64 `json:"max_mb"`
	Count   int   `json:"count"`
	TTLMin  int64 `json:"ttl_min"`
}

// MonitorData is the response payload for /api/monitor.
type MonitorData struct {
	Files       []FileStatus   `json:"files"`
	Cache       CacheStats     `json:"cache"`
	AllWindows  []WindowStatus `json:"all_windows"`
	HeapInuseMB int64          `json:"heap_inuse_mb"`
}

// WindowCache is a shared, size-bounded LRU cache of prefetch windows.
// Windows survive file close and are reused on re-open.
type WindowCache struct {
	mu        sync.Mutex
	windows   map[string]*CachedWindow // key: url+"|"+start
	dataBytes int64                    // bytes of WindowReady windows (guarded by mu)
	maxBytes  int64
	ttl       time.Duration
	dl        *Downloader
	ctx       context.Context
	cancel    context.CancelFunc
}

func newWindowCache(dl *Downloader, maxBytes int64, ttl time.Duration) *WindowCache {
	ctx, cancel := context.WithCancel(context.Background())
	wc := &WindowCache{
		windows:  make(map[string]*CachedWindow),
		maxBytes: maxBytes,
		ttl:      ttl,
		dl:       dl,
		ctx:      ctx,
		cancel:   cancel,
	}
	go wc.sweeper()
	return wc
}

func wKey(url string, start int64) string {
	return fmt.Sprintf("%s|%d", url, start)
}

// GetOrCreate returns an existing window (hit) or creates and starts fetching a new one (miss).
func (wc *WindowCache) GetOrCreate(url string, headers map[string]string, start, windowSize, fileSize int64) *CachedWindow {
	key := wKey(url, start)
	now := time.Now().UnixNano()

	wc.mu.Lock()
	if w, ok := wc.windows[key]; ok {
		atomic.StoreInt64(&w.lastAccess, now)
		wc.mu.Unlock()
		return w
	}

	size := windowSize
	if start+size > fileSize {
		size = fileSize - start
	}
	w := &CachedWindow{
		URL:   url,
		Start: start,
		Size:  size,
		done:  make(chan struct{}),
		state: WindowFetching,
	}
	atomic.StoreInt64(&w.lastAccess, now)
	wc.windows[key] = w
	wc.mu.Unlock()

	go wc.fetch(w, headers)
	return w
}

// Remove drops a specific window from the cache.
func (wc *WindowCache) Remove(url string, start int64) {
	key := wKey(url, start)
	wc.mu.Lock()
	if w, ok := wc.windows[key]; ok {
		delete(wc.windows, key)
		if w.state == WindowReady {
			wc.dataBytes -= w.Size
		}
	}
	wc.mu.Unlock()
}

// PurgeURL drops all windows for a URL.
func (wc *WindowCache) PurgeURL(url string) {
	prefix := url + "|"
	wc.mu.Lock()
	for k, w := range wc.windows {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(wc.windows, k)
			if w.state == WindowReady {
				wc.dataBytes -= w.Size
			}
		}
	}
	wc.mu.Unlock()
}

// DataBytes returns current bytes of ready windows.
func (wc *WindowCache) DataBytes() int64 {
	wc.mu.Lock()
	b := wc.dataBytes
	wc.mu.Unlock()
	return b
}

// Stats returns a snapshot for monitoring.
func (wc *WindowCache) Stats() CacheStats {
	wc.mu.Lock()
	count := 0
	for _, w := range wc.windows {
		if w.state == WindowReady {
			count++
		}
	}
	b, max := wc.dataBytes, wc.maxBytes
	wc.mu.Unlock()
	return CacheStats{
		DataMB: b / (1024 * 1024),
		MaxMB:  max / (1024 * 1024),
		Count:  count,
		TTLMin: int64(wc.ttl.Minutes()),
	}
}

// PurgeByAge removes all ready windows whose age fraction (elapsed/TTL) >= minAgeFrac.
// Pass 0.0 to purge everything, 0.5 for orange+red, 0.9 for red only.
// Returns the number of windows removed.
func (wc *WindowCache) PurgeByAge(minAgeFrac float64) int {
	now := time.Now().UnixNano()
	wc.mu.Lock()
	defer wc.mu.Unlock()
	ttlNs := wc.ttl.Nanoseconds()
	if ttlNs <= 0 {
		ttlNs = 1
	}
	removed := 0
	for k, w := range wc.windows {
		if w.state != WindowReady {
			continue
		}
		la := atomic.LoadInt64(&w.lastAccess)
		ageFrac := float64(now-la) / float64(ttlNs)
		if ageFrac >= minAgeFrac {
			delete(wc.windows, k)
			wc.dataBytes -= w.Size
			removed++
		}
	}
	return removed
}

// SetLimits updates the max cache size and TTL at runtime (takes effect immediately).
func (wc *WindowCache) SetLimits(maxBytes int64, ttl time.Duration) {
	wc.mu.Lock()
	wc.maxBytes = maxBytes
	wc.ttl = ttl
	wc.mu.Unlock()
}

// AllStatus returns WindowStatus for every cached window, sorted oldest-first.
func (wc *WindowCache) AllStatus() []WindowStatus {
	now := time.Now().UnixNano()
	wc.mu.Lock()
	ttlNs := wc.ttl.Nanoseconds()
	result := make([]WindowStatus, 0, len(wc.windows))
	for _, w := range wc.windows {
		la := atomic.LoadInt64(&w.lastAccess)
		lastAgoSec := (now - la) / int64(time.Second)
		var expiresIn int64
		errStr := ""
		if w.state == WindowReady {
			expiresIn = (ttlNs - (now - la)) / int64(time.Second)
			if expiresIn < 0 {
				expiresIn = 0
			}
		}
		if w.err != nil {
			errStr = w.err.Error()
		}
		result = append(result, WindowStatus{
			Start:         w.Start,
			End:           w.Start + w.Size - 1,
			State:         w.state,
			Error:         errStr,
			LastAccessAgo: lastAgoSec,
			ExpiresIn:     expiresIn,
		})
	}
	wc.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].LastAccessAgo > result[j].LastAccessAgo })
	return result
}

// StatusForURL returns WindowStatus for all cached windows matching a URL.
func (wc *WindowCache) StatusForURL(url string) []WindowStatus {
	prefix := url + "|"
	now := time.Now().UnixNano()
	ttlNs := wc.ttl.Nanoseconds()

	wc.mu.Lock()
	defer wc.mu.Unlock()

	var result []WindowStatus
	for k, w := range wc.windows {
		if len(k) <= len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		la := atomic.LoadInt64(&w.lastAccess)
		lastAgoSec := (now - la) / int64(time.Second)
		var expiresIn int64
		errStr := ""
		if w.state == WindowReady {
			expiresIn = (ttlNs - (now - la)) / int64(time.Second)
			if expiresIn < 0 {
				expiresIn = 0
			}
		}
		if w.err != nil {
			errStr = w.err.Error()
		}
		result = append(result, WindowStatus{
			Start:         w.Start,
			End:           w.Start + w.Size - 1,
			State:         w.state,
			Error:         errStr,
			LastAccessAgo: lastAgoSec,
			ExpiresIn:     expiresIn,
		})
	}
	return result
}

// HeapStats returns Go runtime memory usage.
func HeapStats() int64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapInuse) / (1024 * 1024)
}

// --- fetch goroutine ---

func (wc *WindowCache) fetch(w *CachedWindow, headers map[string]string) {
	defer close(w.done)

	chunkSize := int64(wc.dl.ChunkSizeMB) * 1024 * 1024
	if chunkSize <= 0 {
		chunkSize = 8 * 1024 * 1024
	}

	type sub struct {
		index      int
		start, end int64
		data       []byte
		err        error
	}

	var subs []sub
	for i, s := 0, w.Start; s < w.Start+w.Size; i, s = i+1, s+chunkSize {
		e := s + chunkSize - 1
		if e >= w.Start+w.Size {
			e = w.Start + w.Size - 1
		}
		subs = append(subs, sub{index: i, start: s, end: e})
	}

	parallelism := wc.dl.ParallelChunks
	if parallelism <= 0 {
		parallelism = 1
	}
	sem := make(chan struct{}, parallelism)
	results := make([]sub, len(subs))
	var wg sync.WaitGroup

	for i := range subs {
		select {
		case <-wc.ctx.Done():
			wg.Wait()
			wc.setError(w, wc.ctx.Err())
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(sc *sub) {
			defer wg.Done()
			defer func() { <-sem }()
			sc.data, sc.err = wc.dl.fetchChunk(w.URL, sc.start, sc.end, headers)
			results[sc.index] = *sc
		}(&subs[i])
	}
	wg.Wait()

	select {
	case <-wc.ctx.Done():
		wc.setError(w, wc.ctx.Err())
		return
	default:
	}

	var buf []byte
	for _, sc := range results {
		if sc.err != nil {
			// Remove from cache on error so next open re-tries
			wc.Remove(w.URL, w.Start)
			wc.setError(w, fmt.Errorf("sub-chunk %d: %w", sc.index, sc.err))
			return
		}
		buf = append(buf, sc.data...)
	}

	now := time.Now().UnixNano()
	wc.mu.Lock()
	w.data = buf
	w.state = WindowReady
	w.fetchedAt = now
	wc.dataBytes += w.Size
	wc.mu.Unlock()
	atomic.StoreInt64(&w.lastAccess, now)

	if wc.DataBytes() > wc.maxBytes {
		go wc.evict()
	}
}

func (wc *WindowCache) setError(w *CachedWindow, err error) {
	wc.mu.Lock()
	w.err = err
	w.state = WindowError
	wc.mu.Unlock()
}

// --- eviction ---

func (wc *WindowCache) evict() {
	type candidate struct {
		key  string
		la   int64
		size int64
	}

	wc.mu.Lock()
	if wc.dataBytes <= wc.maxBytes {
		wc.mu.Unlock()
		return
	}
	candidates := make([]candidate, 0, len(wc.windows))
	for k, w := range wc.windows {
		if w.state == WindowReady {
			candidates = append(candidates, candidate{k, atomic.LoadInt64(&w.lastAccess), w.Size})
		}
	}
	wc.mu.Unlock()

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].la < candidates[j].la })

	wc.mu.Lock()
	for _, c := range candidates {
		if wc.dataBytes <= wc.maxBytes {
			break
		}
		if w, ok := wc.windows[c.key]; ok && w.state == WindowReady {
			delete(wc.windows, c.key)
			wc.dataBytes -= w.Size
		}
	}
	wc.mu.Unlock()
}

func (wc *WindowCache) sweeper() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-wc.ctx.Done():
			return
		case <-ticker.C:
			wc.sweepExpired()
		}
	}
}

func (wc *WindowCache) sweepExpired() {
	cutoff := time.Now().Add(-wc.ttl).UnixNano()
	wc.mu.Lock()
	for k, w := range wc.windows {
		if w.state == WindowReady && atomic.LoadInt64(&w.lastAccess) < cutoff {
			delete(wc.windows, k)
			wc.dataBytes -= w.Size
		}
	}
	wc.mu.Unlock()
}

// Close stops all background activity and clears the cache.
func (wc *WindowCache) Close() {
	wc.cancel()
	wc.mu.Lock()
	wc.windows = make(map[string]*CachedWindow)
	wc.dataBytes = 0
	wc.mu.Unlock()
}
