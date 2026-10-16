package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// BotHeader names the calling bot on internal requests.
const BotHeader = "X-Jantaxi-Bot"

// RequireService authenticates the bots on the internal listener:
// "Authorization: Bearer <secret>" (constant-time) plus X-Jantaxi-Bot set to
// "passenger" or "driver", which is stored with WithBot. An empty secret
// rejects every request.
func RequireService(secret string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(secret))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			got := sha256.Sum256([]byte(token))
			// Hashing first makes the comparison independent of the token length.
			if secret == "" || !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			bot := r.Header.Get(BotHeader)
			if bot != string(AppPassenger) && bot != string(AppDriver) {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			next.ServeHTTP(w, r.WithContext(WithBot(r.Context(), bot)))
		})
	}
}
