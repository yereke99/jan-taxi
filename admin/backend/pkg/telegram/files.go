package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxDownloadBytes is the Bot API limit for files bots can download.
const MaxDownloadBytes = 20 << 20

// downloadTimeout bounds one file download.
const downloadTimeout = 60 * time.Second

// ErrFileTooBig is returned by DownloadFile when the file exceeds the limit.
var ErrFileTooBig = errors.New("telegram: file is too big")

// errBadFilePath rejects paths that are not plain relative file paths.
var errBadFilePath = errors.New("telegram: invalid file path")

// DownloadFile fetches a file prepared by GetFile, reading at most maxBytes
// (ErrFileTooBig beyond). The download URL contains the bot token; it is
// never logged and never part of a returned error.
func (c *Client) DownloadFile(ctx context.Context, filePath string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > MaxDownloadBytes {
		maxBytes = MaxDownloadBytes
	}
	segments := strings.Split(filePath, "/")
	if filePath == "" || strings.HasPrefix(filePath, "/") || len(filePath) > 512 {
		return nil, errBadFilePath
	}
	for i, seg := range segments {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "\\?#\x00") {
			return nil, errBadFilePath
		}
		segments[i] = url.PathEscape(seg)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/file/bot"+c.token+"/"+strings.Join(segments, "/"), nil)
	if err != nil {
		return nil, c.transportError("downloadFile", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.transportError("downloadFile", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("telegram downloadFile: %w", &APIError{
			Code: resp.StatusCode, Description: http.StatusText(resp.StatusCode), RetryAfter: headerRetryAfter(resp.Header),
		})
	}
	if resp.ContentLength > maxBytes {
		return nil, ErrFileTooBig
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, c.transportError("downloadFile", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrFileTooBig
	}
	c.log.Debug("telegram file downloaded", slog.Int("bytes", len(data)), slog.Duration("dur", time.Since(start)))
	return data, nil
}
