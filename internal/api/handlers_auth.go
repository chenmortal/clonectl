package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"rclone_sync/internal/auth"
	"rclone_sync/internal/database"
)

type loginIn struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login issues a JWT for valid credentials. Unknown user, bad password, and
// disabled account collapse to the same 401 (no account-existence leak).
func (d *Deps) Login(c *gin.Context) {
	var in loginIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	var user database.User
	err := d.DB.Where("username = ?", in.Username).First(&user).Error
	if err != nil || user.DisabledAt != nil || !auth.VerifyPassword(in.Password, user.PasswordHash) {
		c.Header("WWW-Authenticate", "Bearer")
		AbortDetail(c, http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
		return
	}

	now := database.NowUTC()
	user.LastLoginAt = &now
	d.DB.Save(&user)

	token, tokErr := auth.CreateToken(
		d.Cfg.JWTSecret, d.Cfg.JWTAlgorithm,
		time.Duration(d.Cfg.JWTExpiresMin)*time.Minute,
		auth.Claims{UserID: user.ID, Username: user.Username, Role: user.Role},
	)
	if tokErr != nil {
		AbortDetail(c, http.StatusInternalServerError, "token create failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
		"token_type":   "bearer",
		"expires_in":   d.Cfg.JWTExpiresMin * 60,
		"role":         user.Role,
	})
}

// Me returns the authenticated user's own row.
func (d *Deps) Me(c *gin.Context) {
	user := CurrentUser(c)
	c.JSON(http.StatusOK, gin.H{
		"id":            user.ID,
		"username":      user.Username,
		"role":          user.Role,
		"last_login_at": NaiveUTCPtr(user.LastLoginAt),
	})
}

type changePasswordIn struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword re-authenticates with old_password, then rotates the hash.
func (d *Deps) ChangePassword(c *gin.Context) {
	var in changePasswordIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("new_password", in.NewPassword, StrOpt{Required: true, Min: 8, Max: 128})
	if v.Abort(c) {
		return
	}

	user := CurrentUser(c)
	if !auth.VerifyPassword(in.OldPassword, user.PasswordHash) {
		AbortDetail(c, http.StatusUnauthorized, gin.H{
			"error": "invalid_credentials", "reason": "old_password mismatch",
		})
		return
	}
	hash, err := auth.HashPassword(in.NewPassword, d.Cfg.BcryptRounds)
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, "hash failed")
		return
	}
	user.PasswordHash = hash
	d.DB.Save(user)
	c.Status(http.StatusNoContent)
}
