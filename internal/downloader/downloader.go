package downloader

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Downloader fetches a remote file in parallel chunks via HTTP Range requests.
type Downloader struct {
	ParallelChunks    int
	ChunkSizeMB       int
	BufferSizeKB      int
	ReadAheadWindowMB int
	ReadAheadWindows  int
	MaxCacheMB        int
	CacheTTLMin       int
	HTTPClient        *http.Client
	WindowCache       *WindowCache       // initialised by Init()
	sf                singleflight.Group // deduplicates concurrent identical chunk fetches
}

// Init creates the WindowCache. Must be called once after all fields are set.
func (d *Downloader) Init() {
	maxBytes := int64(d.MaxCacheMB) * 1024 * 1024
	ttl := time.Duration(d.CacheTTLMin) * time.Minute
	d.WindowCache = newWindowCache(d, maxBytes, ttl)
}

type chunk struct {
	index int
	start int64
	end   int64
	data  []byte
	err   error
}

// Download fetches url into w using parallel chunks.
// fileSize must be known in advance (from cache / connector.GetFileSize).
func (d *Downloader) Download(url string, fileSize int64, headers map[string]string, w io.Writer) error {
	chunkSize := int64(d.ChunkSizeMB) * 1024 * 1024

	// Build chunk list
	var chunks []chunk
	for i, start := 0, int64(0); start < fileSize; i, start = i+1, start+chunkSize {
		end := start + chunkSize - 1
		if end >= fileSize {
			end = fileSize - 1
		}
		chunks = append(chunks, chunk{index: i, start: start, end: end})
	}

	sem := make(chan struct{}, d.ParallelChunks)
	results := make([]chunk, len(chunks))
	var wg sync.WaitGroup

	for i := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(c *chunk) {
			defer wg.Done()
			defer func() { <-sem }()
			c.data, c.err = d.fetchChunk(url, c.start, c.end, headers)
			results[c.index] = *c
		}(&chunks[i])
	}
	wg.Wait()

	// Write in order
	for _, c := range results {
		if c.err != nil {
			return fmt.Errorf("chunk %d: %w", c.index, c.err)
		}
		if _, err := w.Write(c.data); err != nil {
			return err
		}
	}
	return nil
}

// ReadAt fetches a single range — used by the VFS Read callback for streaming.
func (d *Downloader) ReadAt(url string, headers map[string]string, offset, length int64) ([]byte, error) {
	return d.fetchChunk(url, offset, offset+length-1, headers)
}

func (d *Downloader) fetchChunk(url string, start, end int64, headers map[string]string) ([]byte, error) {
	// Deduplicate concurrent requests for the exact same byte range.
	// Key includes headers that affect the response (e.g. auth tokens via URL params).
	key := fmt.Sprintf("%s|%d-%d", url, start, end)
	v, err, _ := d.sf.Do(key, func() (interface{}, error) {
		return d.doFetch(url, start, end, headers)
	})
	if err != nil {
		return nil, err
	}
	// Return a copy so callers can't mutate the shared slice.
	src := v.([]byte)
	out := make([]byte, len(src))
	copy(out, src)
	return out, nil
}

func (d *Downloader) doFetch(url string, start, end int64, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	bufSize := d.BufferSizeKB * 1024
	if bufSize <= 0 {
		bufSize = 256 * 1024
	}
	buf := make([]byte, 0, end-start+1)
	tmp := make([]byte, bufSize)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}
