package auth

import (
	"strings"
	"testing"
)

var cheap = Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerify(t *testing.T) {
	h, err := HashPasswordParams("s3cret", cheap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected format %q", h)
	}
	if err := VerifyPassword(h, "s3cret"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyPassword(h, "S3cret"); err != ErrMismatch {
		t.Fatalf("wrong password: %v", err)
	}
	h2, _ := HashPasswordParams("s3cret", cheap)
	if h == h2 {
		t.Fatal("salts must differ")
	}
}

func TestDefaultParams(t *testing.T) {
	h, err := HashPassword("pw")
	if err != nil || VerifyPassword(h, "pw") != nil {
		t.Fatalf("default params: %v", err)
	}
}

func TestPlaintextAndMalformed(t *testing.T) {
	if VerifyPassword("plaintext:abc", "abc") != nil || VerifyPassword("plaintext:abc", "abd") != ErrMismatch {
		t.Fatal("plaintext handling")
	}
	for _, bad := range []string{"", "$bcrypt$x", "$argon2id$v=18$m=1,t=1,p=1$AA$AA", "$argon2id$v=19$m=x$AA$AA"} {
		if VerifyPassword(bad, "x") == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func BenchmarkVerifyDefault(b *testing.B) {
	h, _ := HashPassword("password")
	for b.Loop() {
		_ = VerifyPassword(h, "password")
	}
}
