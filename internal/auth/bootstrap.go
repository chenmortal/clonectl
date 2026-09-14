package auth

import (
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
)

// BootstrapAdmin creates the bootstrap admin iff the users table is empty
// and both env vars are set. Idempotent; failures are logged, never fatal.
func BootstrapAdmin(db *gorm.DB, settings config.Settings) {
	user := strings.TrimSpace(settings.BootstrapAdminUser)
	password := settings.BootstrapAdminPassword
	if user == "" || password == "" {
		return
	}

	var count int64
	if err := db.Model(&database.User{}).Count(&count).Error; err != nil {
		slog.Error("bootstrap_admin: count users failed", "err", err)
		return
	}
	if count > 0 {
		return
	}

	hash, err := HashPassword(password, settings.BcryptRounds)
	if err != nil {
		slog.Error("bootstrap_admin: hash failed", "err", err)
		return
	}
	admin := database.User{Username: user, PasswordHash: hash, Role: database.RoleAdmin}
	if err := db.Create(&admin).Error; err != nil {
		slog.Error("bootstrap_admin: create failed", "err", err)
		return
	}
	slog.Warn(
		"bootstrap_admin: created admin user; change the password via POST /api/auth/change-password as soon as possible",
		"username", admin.Username, "id", admin.ID,
	)
}
