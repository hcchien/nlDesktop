package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h := HashPassword("password123")
	if !IsHashed(h) {
		t.Fatalf("hash %q not recognized as hashed", h)
	}
	if !VerifyPassword(h, "password123") {
		t.Error("correct password rejected")
	}
	if VerifyPassword(h, "wrong") {
		t.Error("wrong password accepted")
	}
	if HashPassword("password123") == h {
		t.Error("hashes should be salted (two hashes of same password equal)")
	}
}

func TestTokenSignAndParse(t *testing.T) {
	secret := []byte("s3cret")
	tok := SignToken(secret, 42, time.Hour)

	uid, err := ParseToken(secret, tok)
	if err != nil || uid != 42 {
		t.Fatalf("ParseToken = %d, %v; want 42, nil", uid, err)
	}
	if _, err := ParseToken([]byte("other"), tok); err == nil {
		t.Error("token accepted with wrong secret")
	}
	if _, err := ParseToken(secret, tok+"x"); err == nil {
		t.Error("tampered token accepted")
	}
	expired := SignToken(secret, 42, -time.Minute)
	if _, err := ParseToken(secret, expired); err == nil {
		t.Error("expired token accepted")
	}
}

func TestAPIKey(t *testing.T) {
	plain, hash := NewAPIKey()
	if !IsAPIKey(plain) || !strings.HasPrefix(plain, "nlk_") {
		t.Errorf("unexpected key format %q", plain)
	}
	if HashAPIKey(plain) != hash {
		t.Error("hash mismatch")
	}
	plain2, _ := NewAPIKey()
	if plain == plain2 {
		t.Error("keys should be random")
	}
}
