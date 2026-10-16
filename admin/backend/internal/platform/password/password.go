// Package password hashes administrator passwords with argon2id and stores them
// in the PHC string format.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parameters follow the OWASP argon2id baseline (m=64 MiB, t=3, p=2).
const (
	memoryKiB uint32 = 64 * 1024
	timeCost  uint32 = 3
	threads   uint8  = 2
	saltLen          = 16
	keyLen    uint32 = 32
)

// ErrMalformed is returned for hashes that are not argon2id PHC strings.
var ErrMalformed = errors.New("password: malformed hash")

// Hash returns an encoded argon2id hash of pw.
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, timeCost, memoryKiB, threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, timeCost, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches the encoded hash, in constant time.
func Verify(pw, encoded string) (bool, error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, p.t, p.m, p.p, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// NeedsRehash reports whether encoded uses weaker parameters than the current ones.
func NeedsRehash(encoded string) bool {
	p, _, _, err := decode(encoded)
	if err != nil {
		return true
	}
	return p.m < memoryKiB || p.t < timeCost || p.p < threads
}

type params struct {
	m, t uint32
	p    uint8
}

func decode(encoded string) (params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params{}, nil, nil, ErrMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params{}, nil, nil, ErrMalformed
	}
	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.m, &p.t, &p.p); err != nil || p.t == 0 || p.p == 0 {
		return params{}, nil, nil, ErrMalformed
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return params{}, nil, nil, ErrMalformed
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return params{}, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}
