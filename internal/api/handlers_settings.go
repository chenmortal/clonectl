package api

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"clonectl/internal/database"
	"clonectl/internal/services"
)

func toSettingOut(s *database.SystemSetting) gin.H {
	return gin.H{
		"key": s.Key, "value": s.Value,
		"updated_at": NaiveUTC(s.UpdatedAt), "updated_by_user_id": intNil(s.UpdatedByUserID),
	}
}

// ListSettings (admin) — ordered by key.
func (d *Deps) ListSettings(c *gin.Context) {
	var rows []database.SystemSetting
	if err := d.DB.Order("setting_key").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toSettingOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

// GetSetting (admin).
func (d *Deps) GetSetting(c *gin.Context) {
	var row database.SystemSetting
	if err := d.DB.First(&row, "setting_key = ?", c.Param("key")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "setting not found")
		return
	}
	c.JSON(http.StatusOK, toSettingOut(&row))
}

// SiteInfo is public (pre-auth): the configurable brand title used by the
// login page, the app shell and the browser tab. Empty value = frontend default.
func (d *Deps) SiteInfo(c *gin.Context) {
	var row database.SystemSetting
	title := ""
	if err := d.DB.First(&row, "setting_key = ?", database.SettingSiteTitle).Error; err == nil {
		title = row.Value
	}
	c.JSON(http.StatusOK, gin.H{"site_title": title})
}

// maxSiteTitleRunes caps the brand title (rune count, not bytes).
const maxSiteTitleRunes = 100

// UpsertSetting (admin) — records updated_by; alertmanager_url must be a
// valid http(s) URL when non-empty; site_title is trimmed and capped.
func (d *Deps) UpsertSetting(c *gin.Context) {
	var in struct {
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	key := c.Param("key")
	if key == database.SettingSiteTitle {
		in.Value = strings.TrimSpace(in.Value)
		if utf8.RuneCountInString(in.Value) > maxSiteTitleRunes {
			AbortDetail(c, http.StatusUnprocessableEntity,
				"site_title is limited to 100 characters")
			return
		}
	} else {
		v := NewValidator()
		v.Str("value", in.Value, StrOpt{Min: 0, Max: 4096})
		if v.Abort(c) {
			return
		}
	}
	if key == database.SettingAlertmanagerURL && in.Value != "" {
		if msg := validateAlertmanagerURL(in.Value); msg != "" {
			AbortDetail(c, http.StatusUnprocessableEntity, msg)
			return
		}
	}

	user := CurrentUser(c)
	var row database.SystemSetting
	if err := d.DB.First(&row, "setting_key = ?", key).Error; err != nil {
		row = database.SystemSetting{Key: key, Value: in.Value}
	} else {
		row.Value = in.Value
	}
	if user != nil {
		row.UpdatedByUserID = &user.ID
	}
	if err := d.DB.Save(&row).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, toSettingOut(&row))
}

func validateAlertmanagerURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "alertmanager_url is not a valid URL: " + err.Error()
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "alertmanager_url scheme must be http or https, got '" + u.Scheme + "'"
	}
	return ""
}

type alertmanagerTestIn struct {
	Alertname string            `json:"alertname"`
	Labels    map[string]string `json:"labels"`
}

// AlertmanagerTest (admin) sends a synthetic firing webhook.
func (d *Deps) AlertmanagerTest(c *gin.Context) {
	var in alertmanagerTestIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	if in.Alertname == "" {
		in.Alertname = "ManualTest"
	}
	url := services.GetAlertmanagerURL(d.DB)
	if url == "" {
		AbortDetail(c, http.StatusBadRequest,
			"alertmanager_url not configured; set it first via PUT /api/system-settings/alertmanager_url")
		return
	}
	labels := map[string]string{"alertname": in.Alertname}
	for k, v := range in.Labels {
		labels[k] = strings.TrimSpace(v)
	}
	payload := services.BuildAlertmanagerPayload(0, in.Alertname, 0, "manual test alert",
		nil, "firing", labels, true, nil)
	sent, code, errMsg := services.PostToAlertmanager(url, payload)
	c.JSON(http.StatusOK, gin.H{"sent": sent, "status_code": code, "error": errNil(errMsg)})
}

func errNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
