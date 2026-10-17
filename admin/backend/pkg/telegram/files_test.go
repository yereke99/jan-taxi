package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fileServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getFile":
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Contains(raw, []byte(`"file_id":"F1"`)) {
				t.Errorf("getFile body %s", raw)
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"F1","file_unique_id":"U1","file_size":11,"file_path":"documents/file 1.pdf"}}`)
		case "/file/bot" + testToken + "/documents/file 1.pdf":
			_, _ = w.Write(content)
		case "/file/bot" + testToken + "/documents/stream.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.(http.Flusher).Flush() // no Content-Length: the limit applies while reading
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetFileAndDownload(t *testing.T) {
	content := []byte("%PDF-1.7 hi")
	srv := fileServer(t, content)
	c := New(testToken, WithBaseURL(srv.URL))
	ctx := context.Background()
	f, err := c.GetFile(ctx, "F1")
	if err != nil {
		t.Fatal(err)
	}
	if f.FilePath != "documents/file 1.pdf" || f.FileSize != 11 {
		t.Fatalf("file = %+v", f)
	}
	data, err := c.DownloadFile(ctx, f.FilePath, 1024)
	if err != nil || !bytes.Equal(data, content) {
		t.Fatalf("download = %q, %v", data, err)
	}
	if _, err := c.DownloadFile(ctx, f.FilePath, 5); !errors.Is(err, ErrFileTooBig) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := c.DownloadFile(ctx, "documents/stream.pdf", 5); !errors.Is(err, ErrFileTooBig) {
		t.Fatalf("streamed over the limit: %v", err)
	}
	for _, p := range []string{"", "/etc/passwd", "../bot/getMe", "documents/../x", "a//b", "a?x=1"} {
		if _, err := c.DownloadFile(ctx, p, 1024); !errors.Is(err, errBadFilePath) {
			t.Errorf("path %q: %v", p, err)
		}
	}
	_, err = c.DownloadFile(ctx, "documents/missing.pdf", 1024)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != http.StatusNotFound || strings.Contains(err.Error(), testToken) {
		t.Fatalf("missing file: %v", err)
	}
	srv.Close()
	_, err = c.DownloadFile(ctx, f.FilePath, 1024)
	if err == nil || strings.Contains(err.Error(), testToken) || !IsRetryable(err) {
		t.Fatalf("transport error leaks or is not retryable: %v", err)
	}
}
