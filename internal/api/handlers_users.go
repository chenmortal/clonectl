package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"clonectl/internal/auth"
	"clonectl/internal/database"
)

type userOut struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	Role        string  `json:"role"`
	CreatedAt   string  `json:"created_at"`
	LastLoginAt *string `json:"last_login_at"`
	DisabledAt  *string `json:"disabled_at"`
}

func toUserOut(u *database.User) userOut {
	return userOut{
		ID:          u.ID,
		Username:    u.Username,
		Role:        u.Role,
		CreatedAt:   NaiveUTC(u.CreatedAt),
		LastLoginAt: NaiveUTCPtr(u.LastLoginAt),
		DisabledAt:  NaiveUTCPtr(u.DisabledAt),
	}
}

// ListUsers (admin) — ordered by id.
func (d *Deps) ListUsers(c *gin.Context) {
	var rows []database.User
	if err := d.DB.Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userOut, 0, len(rows))
	for i := range rows {
		out = append(out, toUserOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

type userCreateIn struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// CreateUser (admin) → 201.
func (d *Deps) CreateUser(c *gin.Context) {
	var in userCreateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("username", in.Username, StrOpt{Required: true, Min: 1, Max: 64})
	v.Str("password", in.Password, StrOpt{Required: true, Min: 8, Max: 128})
	if !v.OK() {
		// Role validated first in Python (route layer) — check before 422s.
		if in.Role != "" && !validRole(in.Role) {
			AbortErr(c, http.StatusBadRequest, gin.H{
				"error": "invalid_role", "value": in.Role, "allowed": database.Roles,
			})
			return
		}
		v.Abort(c)
		return
	}
	if !validRole(in.Role) {
		AbortErr(c, http.StatusBadRequest, gin.H{
			"error": "invalid_role", "value": in.Role, "allowed": database.Roles,
		})
		return
	}

	var count int64
	d.DB.Model(&database.User{}).Where("username = ?", in.Username).Count(&count)
	if count > 0 {
		AbortDetail(c, http.StatusConflict, gin.H{"error": "username_taken", "username": in.Username})
		return
	}
	hash, err := auth.HashPassword(in.Password, d.Cfg.BcryptRounds)
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, "hash failed")
		return
	}
	user := database.User{Username: in.Username, PasswordHash: hash, Role: in.Role}
	if err := d.DB.Create(&user).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, toUserOut(&user))
}

func validRole(r string) bool {
	for _, x := range database.Roles {
		if x == r {
			return true
		}
	}
	return false
}

type userUpdateIn struct {
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

// UpdateUser (admin) — partial update of role / disabled.
func (d *Deps) UpdateUser(c *gin.Context) {
	var in userUpdateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	if in.Role != nil && !validRole(*in.Role) {
		AbortErr(c, http.StatusBadRequest, gin.H{
			"error": "invalid_role", "value": *in.Role, "allowed": database.Roles,
		})
		return
	}
	var user database.User
	if err := d.DB.First(&user, c.Param("user_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "user not found")
		return
	}
	if in.Role != nil {
		user.Role = *in.Role
	}
	if in.Disabled != nil {
		if *in.Disabled {
			now := database.NowUTC()
			user.DisabledAt = &now
		} else {
			user.DisabledAt = nil
		}
	}
	d.DB.Save(&user)
	c.JSON(http.StatusOK, toUserOut(&user))
}

// DeleteUser (admin) — self-deletion is rejected.
func (d *Deps) DeleteUser(c *gin.Context) {
	me := CurrentUser(c)
	var user database.User
	if err := d.DB.First(&user, c.Param("user_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "user not found")
		return
	}
	if me != nil && user.ID == me.ID {
		AbortDetail(c, http.StatusBadRequest, gin.H{"error": "cannot_delete_self"})
		return
	}
	d.DB.Delete(&user)
	c.Status(http.StatusNoContent)
}

type resetPasswordIn struct {
	NewPassword string `json:"new_password"`
}

// ResetPassword (admin) — force-set another user's password.
func (d *Deps) ResetPassword(c *gin.Context) {
	var in resetPasswordIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("new_password", in.NewPassword, StrOpt{Required: true, Min: 8, Max: 128})
	if v.Abort(c) {
		return
	}
	var user database.User
	if err := d.DB.First(&user, c.Param("user_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "user not found")
		return
	}
	hash, err := auth.HashPassword(in.NewPassword, d.Cfg.BcryptRounds)
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, "hash failed")
		return
	}
	user.PasswordHash = hash
	d.DB.Save(&user)
	c.Status(http.StatusNoContent)
}
