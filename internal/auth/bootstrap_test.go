package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
)

func bootstrapDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}

func TestBootstrapCreatesAdminWhenEmpty(t *testing.T) {
	db := bootstrapDB(t)
	s := config.Settings{BootstrapAdminUser: "admin", BootstrapAdminPassword: "Sup3r$ecret!", BcryptRounds: 4}
	BootstrapAdmin(db, s)

	var u database.User
	require.NoError(t, db.Where("username = ?", "admin").First(&u).Error)
	assert.Equal(t, database.RoleAdmin, u.Role)
	assert.True(t, VerifyPassword("Sup3r$ecret!", u.PasswordHash))
}

func TestBootstrapNoopWhenUsersExist(t *testing.T) {
	db := bootstrapDB(t)
	require.NoError(t, db.Create(&database.User{Username: "seed", PasswordHash: "x", Role: "view"}).Error)
	s := config.Settings{BootstrapAdminUser: "admin", BootstrapAdminPassword: "pw", BcryptRounds: 4}
	BootstrapAdmin(db, s)

	var count int64
	db.Model(&database.User{}).Count(&count)
	assert.EqualValues(t, 1, count)
}

func TestBootstrapNoopWhenEnvMissing(t *testing.T) {
	db := bootstrapDB(t)
	BootstrapAdmin(db, config.Settings{BootstrapAdminUser: "admin", BcryptRounds: 4})
	BootstrapAdmin(db, config.Settings{BootstrapAdminPassword: "pw", BcryptRounds: 4})
	BootstrapAdmin(db, config.Settings{})

	var count int64
	db.Model(&database.User{}).Count(&count)
	assert.EqualValues(t, 0, count)
}

func TestBootstrapIdempotent(t *testing.T) {
	db := bootstrapDB(t)
	s := config.Settings{BootstrapAdminUser: "admin", BootstrapAdminPassword: "pw", BcryptRounds: 4}
	BootstrapAdmin(db, s)
	BootstrapAdmin(db, s)

	var count int64
	db.Model(&database.User{}).Count(&count)
	assert.EqualValues(t, 1, count)
}
