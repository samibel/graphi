// Package auth validates bearer tokens. The token format is deliberately
// trivial ("<subject>:<unix-expiry>:<signature>") so the fixture needs no
// cryptography dependency; what matters for the evaluation is that the
// validation logic lives here and nowhere else.
package auth

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrMalformed is returned when a token does not have the three expected parts.
var ErrMalformed = errors.New("auth: malformed token")

// ErrExpired is returned when a token's expiry lies in the past.
var ErrExpired = errors.New("auth: token expired")

// ErrBadSignature is returned when the signature part does not match.
var ErrBadSignature = errors.New("auth: bad signature")

// Claims is the decoded content of a token.
type Claims struct {
	Subject string
	Expiry  time.Time
}

// ParseToken splits a raw token into its claims without checking expiry or
// signature. Callers that need a trust decision must use ValidateToken.
func ParseToken(raw string) (Claims, string, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return Claims{}, "", ErrMalformed
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Claims{}, "", ErrMalformed
	}
	return Claims{Subject: parts[0], Expiry: time.Unix(exp, 0)}, parts[2], nil
}

// ValidateToken is the single trust decision of the fixture: it parses the
// token, rejects an expired one, and checks the signature against secret.
// Every HTTP request passes through here before it reaches the store.
func ValidateToken(raw, secret string, now time.Time) (Claims, error) {
	claims, sig, err := ParseToken(raw)
	if err != nil {
		return Claims{}, err
	}
	if !claims.Expiry.After(now) {
		return Claims{}, ErrExpired
	}
	if sig != sign(claims, secret) {
		return Claims{}, ErrBadSignature
	}
	return claims, nil
}

// Issue mints a token for subject that expires after ttl.
func Issue(subject, secret string, now time.Time, ttl time.Duration) string {
	claims := Claims{Subject: subject, Expiry: now.Add(ttl)}
	return subject + ":" + strconv.FormatInt(claims.Expiry.Unix(), 10) + ":" + sign(claims, secret)
}

// sign derives the signature part deterministically from the claims and the
// secret. It is not cryptographically meaningful and must not be copied.
func sign(c Claims, secret string) string {
	var h uint32 = 2166136261
	for _, b := range []byte(c.Subject + "|" + strconv.FormatInt(c.Expiry.Unix(), 10) + "|" + secret) {
		h ^= uint32(b)
		h *= 16777619
	}
	return strconv.FormatUint(uint64(h), 16)
}
