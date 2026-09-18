package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"clonectl/internal/database"
)

// Legacy /api/storages — GET remains for the deprecation window; writes
// return 410 Gone pointing at storage-sources / data-sources.

func toStorageOut(s *database.StorageConfig) gin.H {
	return gin.H{
		"id": s.ID, "name": s.Name, "type": s.Type, "parameters": s.Parameters,
		"created_at": NaiveUTC(s.CreatedAt), "updated_at": NaiveUTC(s.UpdatedAt),
	}
}

// ListStorages — all roles.
func (d *Deps) ListStorages(c *gin.Context) {
	var rows []database.StorageConfig
	if err := d.DB.Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toStorageOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

// GetStorage — all roles.
func (d *Deps) GetStorage(c *gin.Context) {
	var s database.StorageConfig
	if err := d.DB.First(&s, c.Param("storage_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "storage not found")
		return
	}
	c.JSON(http.StatusOK, toStorageOut(&s))
}

var deprecatedBody = gin.H{
	"error": "deprecated",
	"use":   "/api/storage-sources and /api/data-sources",
}

// CreateStorageDeprecated → 410.
func (d *Deps) CreateStorageDeprecated(c *gin.Context) {
	AbortDetail(c, http.StatusGone, deprecatedBody)
}

// UpdateStorageDeprecated → 410.
func (d *Deps) UpdateStorageDeprecated(c *gin.Context) {
	AbortDetail(c, http.StatusGone, deprecatedBody)
}

// DeleteStorageDeprecated → 410.
func (d *Deps) DeleteStorageDeprecated(c *gin.Context) {
	AbortDetail(c, http.StatusGone, deprecatedBody)
}
