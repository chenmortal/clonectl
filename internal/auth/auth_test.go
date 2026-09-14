package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("s3cret!", 4)
	require.NoError(t, err)
	assert.True(t, VerifyPassword("s3cret!", h))
	assert.False(t, VerifyPassword("wrong", h))
	// Garbage hash → false, never panics.
	assert.False(t, VerifyPassword("s3cret!", "not-a-bcrypt-hash"))
}

func TestJWTRoundtrip(t *testing.T) {
	tok, err := CreateToken("secret", "HS256", time.Hour, Claims{UserID: 7, Username: "bob", Role: "edit"})
	require.NoError(t, err)
	claims, err := ParseToken("secret", "HS256", tok)
	require.NoError(t, err)
	assert.EqualValues(t, 7, claims.UserID)
	assert.Equal(t, "bob", claims.Username)
	assert.Equal(t, "edit", claims.Role)
}

func TestJWTWrongSecret(t *testing.T) {
	tok, _ := CreateToken("secret", "HS256", time.Hour, Claims{UserID: 1, Username: "b", Role: "view"})
	_, err := ParseToken("other", "HS256", tok)
	assert.Error(t, err)
}

func TestJWTExpiry(t *testing.T) {
	tok, _ := CreateToken("secret", "HS256", -time.Minute, Claims{UserID: 1, Username: "b", Role: "view"})
	_, err := ParseToken("secret", "HS256", tok)
	assert.Error(t, err, "expired token must not parse")
}

func TestJWTTampered(t *testing.T) {
	tok, _ := CreateToken("secret", "HS256", time.Hour, Claims{UserID: 1, Username: "b", Role: "view"})
	_, err := ParseToken("secret", "HS256", tok+"x")
	assert.Error(t, err)
}
