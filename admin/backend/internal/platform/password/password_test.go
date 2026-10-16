package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashVerify(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected encoding: %s", h)
	}
	if ok, err := Verify("correct horse battery staple", h); err != nil || !ok {
		t.Fatalf("correct password: %v %v", ok, err)
	}
	if ok, err := Verify("correct horse battery stapler", h); err != nil || ok {
		t.Fatalf("wrong password accepted: %v %v", ok, err)
	}
	h2, _ := Hash("correct horse battery staple")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	if NeedsRehash(h) {
		t.Fatal("current parameters must not need a rehash")
	}
}

func TestMalformedAndWeak(t *testing.T) {
	for _, enc := range []string{
		"",
		"plaintext",
		"$2a$10$abcdefghijklmnopqrstuuvwxyz", // bcrypt
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5", // wrong variant
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=0,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=3,p=2$!!$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$c2hvcnQ", // key too short
	} {
		if _, err := Verify("x", enc); !errors.Is(err, ErrMalformed) {
			t.Errorf("Verify(%q) error = %v, want ErrMalformed", enc, err)
		}
		if !NeedsRehash(enc) {
			t.Errorf("NeedsRehash(%q) must be true", enc)
		}
	}
	weak := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	if !NeedsRehash(weak) {
		t.Fatal("weaker parameters must need a rehash")
	}
}
