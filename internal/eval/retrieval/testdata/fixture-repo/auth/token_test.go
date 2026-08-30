package auth

import (
	"errors"
	"testing"
	"time"
)

func TestValidateToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok := Issue("alice", "s3cret", now, time.Hour)

	claims, err := ValidateToken(tok, "s3cret", now)
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if claims.Subject != "alice" {
		t.Errorf("subject = %q", claims.Subject)
	}
	if _, err := ValidateToken(tok, "s3cret", now.Add(2*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired token: err = %v", err)
	}
	if _, err := ValidateToken(tok, "other", now); !errors.Is(err, ErrBadSignature) {
		t.Errorf("wrong secret: err = %v", err)
	}
	if _, err := ValidateToken("garbage", "s3cret", now); !errors.Is(err, ErrMalformed) {
		t.Errorf("malformed token: err = %v", err)
	}
}
