package api

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"rclone_sync/internal/database"
)

var storageSourceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func toStorageSourceOut(s *database.StorageSource) gin.H {
	endpoint, region := strNil(s.Endpoint), strNil(s.Region)
	return gin.H{
		"id": s.ID, "name": s.Name, "type": s.Type,
		"endpoint": endpoint, "region": region, "extra": s.Extra,
		"created_at": NaiveUTC(s.CreatedAt), "updated_at": NaiveUTC(s.UpdatedAt),
	}
}

func strNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// ListStorageSources — all roles, ordered by id.
func (d *Deps) ListStorageSources(c *gin.Context) {
	var rows []database.StorageSource
	if err := d.DB.Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toStorageSourceOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

type storageSourceCreateIn struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Endpoint *string        `json:"endpoint"`
	Region   *string        `json:"region"`
	Extra    map[string]any `json:"extra"`
}

// CreateStorageSource (leader+admin) → 201.
func (d *Deps) CreateStorageSource(c *gin.Context) {
	var in storageSourceCreateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("name", in.Name, StrOpt{Required: true, Min: 1, Max: 128,
		Pattern: storageSourceNamePattern, PatternDesc: "^[a-zA-Z0-9_-]+$"})
	v.Str("type", in.Type, StrOpt{Required: true, Min: 1, Max: 64})
	if in.Endpoint != nil {
		v.Str("endpoint", *in.Endpoint, StrOpt{Max: 512})
	}
	if in.Region != nil {
		v.Str("region", *in.Region, StrOpt{Max: 64})
	}
	if v.Abort(c) {
		return
	}

	var count int64
	d.DB.Model(&database.StorageSource{}).Where("name = ?", in.Name).Count(&count)
	if count > 0 {
		AbortDetail(c, http.StatusConflict, "storage source name already exists")
		return
	}
	extra := in.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	src := database.StorageSource{
		Name: in.Name, Type: in.Type, Endpoint: in.Endpoint, Region: in.Region,
		Extra: database.JSONObject(extra),
	}
	if err := d.DB.Create(&src).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, toStorageSourceOut(&src))
}

// GetStorageSource — all roles.
func (d *Deps) GetStorageSource(c *gin.Context) {
	var src database.StorageSource
	if err := d.DB.First(&src, c.Param("source_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "storage source not found")
		return
	}
	c.JSON(http.StatusOK, toStorageSourceOut(&src))
}

// UpdateStorageSource (leader+admin) — partial update.
func (d *Deps) UpdateStorageSource(c *gin.Context) {
	var src database.StorageSource
	if err := d.DB.First(&src, c.Param("source_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "storage source not found")
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	p, err := DecodePartial(raw)
	if err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	if name, ok, err := p.Str("name"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && name != nil {
		v.Str("name", *name, StrOpt{Min: 1, Max: 128,
			Pattern: storageSourceNamePattern, PatternDesc: "^[a-zA-Z0-9_-]+$"})
		src.Name = *name
	} else if ok && name == nil {
		// null violates the NOT NULL column; FastAPI would 422 on None for a
		// patterned str field — mirror that via the validator.
		v.add("name", "Input should be a valid string", "string_type")
	}
	if typ, ok, err := p.Str("type"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && typ != nil {
		v.Str("type", *typ, StrOpt{Min: 1, Max: 64})
		src.Type = *typ
	}
	if endpoint, ok, err := p.Str("endpoint"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if endpoint != nil {
			v.Str("endpoint", *endpoint, StrOpt{Max: 512})
		}
		src.Endpoint = endpoint
	}
	if region, ok, err := p.Str("region"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if region != nil {
			v.Str("region", *region, StrOpt{Max: 64})
		}
		src.Region = region
	}
	if extra, ok, err := p.Object("extra"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && extra != nil {
		src.Extra = database.JSONObject(extra)
	}
	if v.Abort(c) {
		return
	}
	if err := d.DB.Save(&src).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, toStorageSourceOut(&src))
}

// DeleteStorageSource (leader+admin) — 409 while referenced by data sources.
func (d *Deps) DeleteStorageSource(c *gin.Context) {
	var src database.StorageSource
	if err := d.DB.First(&src, c.Param("source_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "storage source not found")
		return
	}
	var used int64
	d.DB.Model(&database.DataSource{}).Where("storage_source_id = ?", src.ID).Limit(1).Count(&used)
	if used > 0 {
		AbortDetail(c, http.StatusConflict, "storage source is used by a data source; delete those first")
		return
	}
	d.DB.Delete(&src)
	c.Status(http.StatusNoContent)
}
