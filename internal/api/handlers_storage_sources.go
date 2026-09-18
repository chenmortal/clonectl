package api

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"

	"clonectl/internal/database"
)

var storageSourceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Storage source kind → which fields are valid. Encoded in the handlers as
// well as the frontend form. Adding a backend = add an entry here and a
// matching case in validateSSConstraints / BuildRemoteParameters.
var validStorageTypes = map[string]struct{}{
	"s3":    {},
	"local": {},
	"redis": {},
}

func isValidStorageType(t string) bool {
	_, ok := validStorageTypes[t]
	return ok
}

// DTO shape. The frontend switches on "type" and ignores irrelevant fields.
func toStorageSourceOut(s *database.StorageSource) gin.H {
	endpoint, region, path := strNil(s.Endpoint), strNil(s.Region), strNil(s.Path)
	return gin.H{
		"id": s.ID, "name": s.Name, "type": s.Type,
		"endpoint": endpoint, "region": region, "path": path, "extra": s.Extra,
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
	Path     *string        `json:"path"`
	Extra    map[string]any `json:"extra"`
}

// validateSSConstraints enforces the type-specific contract:
//   - s3: provider in extra; endpoint recommended
//   - local: endpoint/region must be empty; path is the FS prefix
//   - redis: topology lives in extra; s3-only fields are forbidden;
//     passwords go on DataSource (not StorageSource) so multiple DSNs
//     can share one Redis StorageSource template.
func validateSSConstraints(c *gin.Context, in storageSourceCreateIn, v *Validator) {
	if !isValidStorageType(in.Type) {
		v.add("type", "type must be one of: s3, local, redis", "enum")
		return
	}
	switch in.Type {
	case "s3":
		if in.Path != nil {
			v.add("path", "path is for the local backend; omit for s3", "value_error")
		}
		if in.Extra == nil {
			v.add("extra.provider", "provider is required for s3 (AWS / Minio / Alibaba / Tencent / Other)", "missing")
		} else if p, ok := in.Extra["provider"].(string); !ok || p == "" {
			v.add("extra.provider", "provider is required for s3 (AWS / Minio / Alibaba / Tencent / Other)", "missing")
		}
	case "local":
		if in.Endpoint != nil && *in.Endpoint != "" {
			v.add("endpoint", "endpoint is for network backends; omit for local", "value_error")
		}
		if in.Region != nil && *in.Region != "" {
			v.add("region", "region is for network backends; omit for local", "value_error")
		}
	case "redis":
		// Redis has no S3-style endpoint/region; path holds the key prefix.
		if in.Endpoint != nil && *in.Endpoint != "" {
			v.add("endpoint", "endpoint is for s3; omit for redis (use extra.addresses)", "value_error")
		}
		if in.Region != nil && *in.Region != "" {
			v.add("region", "region is for s3; omit for redis", "value_error")
		}
		if in.Extra == nil {
			v.add("extra", "extra is required for redis (topology + addresses)", "missing")
			return
		}
		mode, _ := in.Extra["mode"].(string)
		switch mode {
		case "standalone", "cluster", "sentinel", "proxy":
			// ok
		default:
			v.add("extra.mode", "redis mode must be one of: standalone, cluster, sentinel, proxy", "enum")
			return
		}
		// addresses: required, ≥1
		raw, ok := in.Extra["addresses"].([]any)
		if !ok || len(raw) == 0 {
			v.add("extra.addresses", "at least one host:port is required", "missing")
			return
		}
		for i, a := range raw {
			s, _ := a.(string)
			if s == "" {
				v.add("extra.addresses["+strconv.Itoa(i)+"]", "address must be non-empty host:port", "value_error")
			}
		}
		if mode == "sentinel" {
			if mn, _ := in.Extra["master_name"].(string); mn == "" {
				v.add("extra.master_name", "sentinel topology requires master_name", "missing")
			}
		}
	}
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
	if in.Path != nil {
		v.Str("path", *in.Path, StrOpt{Max: 512})
	}
	validateSSConstraints(c, in, v)
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
		Name: in.Name, Type: in.Type, Endpoint: in.Endpoint, Region: in.Region, Path: in.Path,
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
		v.add("name", "Input should be a valid string", "string_type")
	}
	newType, typeChanged := false, false
	if typ, ok, err := p.Str("type"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && typ != nil {
		v.Str("type", *typ, StrOpt{Min: 1, Max: 64})
		if *typ != src.Type {
			src.Type = *typ
			newType, typeChanged = true, true
		}
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
	if path, ok, err := p.Str("path"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if path != nil {
			v.Str("path", *path, StrOpt{Max: 512})
		}
		src.Path = path
	}
	if extra, ok, err := p.Object("extra"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && extra != nil {
		src.Extra = database.JSONObject(extra)
	}
	// Re-run the type constraint if the type changed (or type fields changed).
	if typeChanged {
		validateSSConstraints(c, storageSourceCreateIn{
			Type: src.Type, Endpoint: src.Endpoint, Region: src.Region, Path: src.Path, Extra: src.Extra,
		}, v)
	}
	if v.Abort(c) {
		return
	}
	_ = newType
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
