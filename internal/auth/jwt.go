package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims carried by access tokens (parity with app/auth/tokens.py).
type Claims struct {
	UserID   int64
	Username string
	Role     string
}

func signingMethod(alg string) jwt.SigningMethod {
	switch alg {
	case "HS384":
		return jwt.SigningMethodHS384
	case "HS512":
		return jwt.SigningMethodHS512
	default:
		return jwt.SigningMethodHS256
	}
}

// CreateToken issues an HS* access token: sub=<id>, username, role, iat, exp.
func CreateToken(secret, alg string, expires time.Duration, c Claims) (string, error) {
	now := time.Now().UTC()
	tok := jwt.NewWithClaims(signingMethod(alg), jwt.MapClaims{
		"sub":      strconv.FormatInt(c.UserID, 10),
		"username": c.Username,
		"role":     c.Role,
		"iat":      now.Unix(),
		"exp":      now.Add(expires).Unix(),
	})
	return tok.SignedString([]byte(secret))
}

// ParseToken validates signature + expiry and returns the claims.
// Any failure (bad sig, wrong secret, expired, malformed sub) errors.
func ParseToken(secret, alg, tokenStr string) (Claims, error) {
	var claims Claims
	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != signingMethod(alg).Alg() {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return claims, err
	}
	mc, ok := tok.Claims.(jwt.MapClaims)
	if !ok || !tok.Valid {
		return claims, errors.New("invalid token claims")
	}
	sub, _ := mc["sub"].(string)
	id, err := strconv.ParseInt(sub, 10, 64)
	if err != nil {
		return claims, errors.New("token sub not an int")
	}
	username, _ := mc["username"].(string)
	role, _ := mc["role"].(string)
	return Claims{UserID: id, Username: username, Role: role}, nil
}
