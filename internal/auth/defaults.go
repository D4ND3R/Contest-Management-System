package auth

import (
	"crypto/rand"
	"strings"
)

// defaultPasswords are the passwords people try first (and that setup
// guides, including our own `make dev`, hand out): an administrator using
// one is made to choose another before doing anything else.
var defaultPasswords = map[string]bool{
	"admin": true, "administrator": true, "password": true, "passw0rd": true, "changeme": true,
	"change-me": true, "cms": true, "root": true, "secret": true, "default": true, "test": true,
	"qwerty": true, "letmein": true, "1234": true, "12345": true, "123456": true, "12345678": true,
	"123456789": true, "admin123": true, "password1": true,
}

// IsDefaultPassword reports whether password is a well-known default or
// the username itself (case-insensitively).
func IsDefaultPassword(username, password string) bool {
	p := strings.ToLower(strings.TrimSpace(password))
	return p == "" || defaultPasswords[p] || p == strings.ToLower(strings.TrimSpace(username))
}

// RandomPassword returns n characters drawn uniformly from an alphabet
// without look-alikes (0/O, 1/l/I): a strong password that is easy to
// copy from a terminal (16 characters carry ~90 bits).
func RandomPassword(n int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 0, n)
	buf := make([]byte, 2*n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err) // crypto/rand never fails on supported systems
		}
		for _, b := range buf {
			// Rejection sampling: no bias towards the first characters.
			if int(b) < 256-256%len(alphabet) && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out)
}
