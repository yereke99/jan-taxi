package storage

import (
	"io"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// ServeImage writes a stored image with headers that keep it out of shared
// caches and stop browsers from sniffing or executing it. It closes rc. A PDF
// (top-up receipt) is served by ServePDF instead.
func ServeImage(w http.ResponseWriter, r *http.Request, f File, rc io.ReadCloser) {
	if f.Mime == MimePDF {
		ServePDF(w, r, f, rc)
		return
	}
	defer rc.Close()
	size := f.SizeBytes
	if st, ok := rc.(interface{ Stat() (fs.FileInfo, error) }); ok {
		if info, err := st.Stat(); err == nil {
			size = info.Size()
		}
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", `inline; filename="`+f.ID.String()+`.jpg"`)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if size > 0 {
		h.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		httpx.Logger(r.Context()).Debug("serve image interrupted", "file_id", f.ID, "err", err)
	}
}
