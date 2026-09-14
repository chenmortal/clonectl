package api

import (
	"encoding/json"
	"fmt"
)

// Partial decodes PATCH-style bodies where "field absent" and "field set to
// null" are different things (pydantic exclude_unset parity). Typed accessors
// return (value, present, error); nil value + present=true means explicit null.
type Partial map[string]json.RawMessage

// DecodePartial reads the raw body; an empty object is valid.
func DecodePartial(raw []byte) (Partial, error) {
	var p Partial
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p == nil {
		p = Partial{}
	}
	return p, nil
}

func (p Partial) Str(key string) (*string, bool, error) {
	raw, ok := p[key]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, true, fmt.Errorf("field %s: %w", key, err)
	}
	return &s, true, nil
}

func (p Partial) Int64(key string) (*int64, bool, error) {
	raw, ok := p[key]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, true, fmt.Errorf("field %s: %w", key, err)
	}
	return &n, true, nil
}

func (p Partial) Bool(key string) (*bool, bool, error) {
	raw, ok := p[key]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, true, fmt.Errorf("field %s: %w", key, err)
	}
	return &b, true, nil
}

// Object decodes a JSON object field; explicit null → (nil, true, nil).
func (p Partial) Object(key string) (map[string]any, bool, error) {
	raw, ok := p[key]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, true, fmt.Errorf("field %s: %w", key, err)
	}
	return m, true, nil
}
