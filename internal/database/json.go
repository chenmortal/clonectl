package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// JSONObject is a string-keyed JSON column that maps to MySQL JSON and
// SQLite TEXT. Nil marshals to SQL NULL (and scans back as nil).
type JSONObject map[string]any

// Value implements driver.Valuer.
func (j JSONObject) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	b, err := json.Marshal(j)
	if err != nil {
		return nil, fmt.Errorf("marshal json column: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner. Malformed JSON is stored raw rather than
// erroring (legacy rows must not break reads).
func (j *JSONObject) Scan(v any) error {
	if v == nil {
		*j = nil
		return nil
	}
	var b []byte
	switch t := v.(type) {
	case []byte:
		b = t
	case string:
		b = []byte(t)
	default:
		return fmt.Errorf("json column: unsupported scan type %T", v)
	}
	out := JSONObject{}
	if err := json.Unmarshal(b, &out); err != nil {
		// Keep raw payload under a key so the data is never lost.
		*j = JSONObject{"raw": string(b)}
		return nil
	}
	*j = out
	return nil
}

// GormDataType is the generic GORM data type.
func (JSONObject) GormDataType() string { return "json" }

// GormDBDataType picks the column type per dialect at AutoMigrate time.
func (JSONObject) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "JSON"
	}
	return "TEXT"
}

// Clone returns a deep-enough copy (top level) so handlers can mutate
// response payloads without touching the persisted map.
func (j JSONObject) Clone() JSONObject {
	out := make(JSONObject, len(j))
	for k, v := range j {
		out[k] = v
	}
	return out
}

// Merge returns a new map with keys from other overwriting j's.
func (j JSONObject) Merge(other JSONObject) JSONObject {
	out := j.Clone()
	for k, v := range other {
		out[k] = v
	}
	return out
}
