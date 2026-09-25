// Package auth contains password hashing (argon2id), signed session tokens
// and CSRF protection shared by the web services.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the argon2id cost parameters.
type Argon2Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultParams follow the OWASP recommendation (19 MiB, t=2, p=1), which
// keeps a login under ~20 ms of CPU — important when a few thousand
// contestants log in within the same minute.
var DefaultParams = Argon2Params{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// slots bounds the argon2 computations running at once: each needs its
// memory parameter (19 MiB by default) and a core for ~20 ms, so a burst of
// logins (a whole contest at the start signal) waits its turn instead of
// exhausting the machine's memory (a thousand at once would need 19 GiB).
var slots = make(chan struct{}, max(2, runtime.GOMAXPROCS(0)))

// maxMemory bounds the memory parameter accepted from a stored hash
// (imported hashes are not trusted to be sane).
const maxMemory = 256 * 1024

// running and peak count the computations in progress (tests).
var running, peak atomic.Int32

func idKey(password, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	slots <- struct{}{}
	r := running.Add(1)
	for {
		old := peak.Load()
		if r <= old || peak.CompareAndSwap(old, r) {
			break
		}
	}
	defer func() {
		running.Add(-1)
		<-slots
	}()
	return argon2.IDKey(password, salt, t, m, p, n)
}

// ErrMismatch is returned when a password does not match its hash.
var ErrMismatch = errors.New("password mismatch")

// HashPassword returns a PHC-formatted argon2id hash:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
func HashPassword(password string) (string, error) {
	return HashPasswordParams(password, DefaultParams)
}

// HashPasswordParams hashes with explicit parameters (tests use cheap ones).
func HashPasswordParams(password string, p Argon2Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := idKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword checks password against a hash produced by HashPassword.
// A "plaintext:" prefixed hash is accepted for imports from systems that
// stored clear passwords (CMS allows this); it is compared in constant time.
func VerifyPassword(hash, password string) error {
	if rest, ok := strings.CutPrefix(hash, "plaintext:"); ok {
		if subtle.ConstantTimeCompare([]byte(rest), []byte(password)) == 1 {
			return nil
		}
		return ErrMismatch
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return errors.New("unsupported password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return errors.New("unsupported argon2 version")
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return errors.New("malformed argon2 parameters")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return errors.New("malformed salt")
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return errors.New("malformed key")
	}
	if p.Memory > maxMemory || p.Iterations > 16 || p.Parallelism == 0 {
		return errors.New("argon2 parameters out of range")
	}
	got := idKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) == 1 {
		return nil
	}
	return ErrMismatch
}
