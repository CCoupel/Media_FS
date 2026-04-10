package downloader

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

// Downloader fetches a remote file in parallel chunks via HTTP Range requests.
type Downloader struct {
	ParallelChunks int
	ChunkSizeMB    int
	BufferSizeKB   int
	HTTPClient     *http.Client
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

	buf := make([]byte, 0, end-start+1)
	tmp := make([]byte, d.BufferSizeKB*1024)
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
