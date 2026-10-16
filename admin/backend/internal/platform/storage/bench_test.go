package storage

import (
	"context"
	"testing"
)

// BenchmarkSanitizePhone measures a typical 12 MP phone photo upload.
func BenchmarkSanitizePhone(b *testing.B) {
	data := encodeJPEG(b, gradient(4032, 3024))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sanitize(context.Background(), data); err != nil {
			b.Fatal(err)
		}
	}
}
