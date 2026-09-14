// Package auth: password hashing, JWT tokens, bootstrap admin.
package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash using the configured cost factor.
func HashPassword(plain string, rounds int) (string, error) {
	if rounds < bcrypt.MinCost {
		rounds = bcrypt.DefaultCost
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), rounds)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(h), nil
}

// VerifyPassword is a constant-time check; returns false on any decode or
// format error (garbage hash).
func VerifyPassword(plain, hashed string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
}
