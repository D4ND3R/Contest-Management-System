package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPRFC6238Vectors(t *testing.T) {
	// RFC 6238 appendix B (SHA1, secret "12345678901234567890"), 6 digits.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		got, err := TOTPCode(secret, time.Unix(ts, 0))
		if err != nil || got != want {
			t.Errorf("t=%d: %s (%v), want %s", ts, got, err, want)
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	s := NewTOTPSecret()
	now := time.Now()
	code, _ := TOTPCode(s, now)
	prev, _ := TOTPCode(s, now.Add(-30*time.Second))
	old, _ := TOTPCode(s, now.Add(-5*time.Minute))
	if !VerifyTOTP(s, code, now) || !VerifyTOTP(s, prev, now) {
		t.Fatal("current or previous code rejected")
	}
	if VerifyTOTP(s, old, now) || VerifyTOTP(s, "12345", now) || VerifyTOTP("!!", code, now) {
		t.Fatal("stale or malformed code accepted")
	}
	if u := TOTPURI("CMS", "admin", s); !strings.HasPrefix(u, "otpauth://totp/CMS:admin?") || !strings.Contains(u, "secret="+s) {
		t.Fatalf("uri %s", u)
	}
}
