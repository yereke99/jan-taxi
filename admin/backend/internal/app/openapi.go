package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/api"
)

var specETag = func() string {
	sum := sha256.Sum256(api.Spec)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}()

// serveOpenAPI serves the embedded contract at /api/v1/openapi.yaml.
func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "application/yaml")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", specETag)
	if r.Header.Get("If-None-Match") == specETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(api.Spec)
}
