package api

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ValidationError mirrors FastAPI's 422 array items: {loc, msg, type}.
type ValidationError struct {
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
	Type string `json:"type"`
}

// Validator accumulates body-field errors and renders them as FastAPI does.
// The frontend only reads detail[0].msg for arrays, so shape is the contract.
type Validator struct {
	errs []ValidationError
}

func NewValidator() *Validator { return &Validator{} }

func (v *Validator) add(field, msg, typ string) {
	v.errs = append(v.errs, ValidationError{Loc: []any{"body", field}, Msg: msg, Type: typ})
}

// StrOpt tunes Str validation.
type StrOpt struct {
	Required    bool
	Min         int // -1 → no min
	Max         int // -1 → no max
	Pattern     *regexp.Regexp
	PatternDesc string
}

// Str validates a string field; returns the value for chaining.
func (v *Validator) Str(field, val string, o StrOpt) string {
	if o.Required && val == "" {
		v.add(field, "Field required", "missing")
		return val
	}
	if val == "" {
		return val
	}
	if o.Min >= 0 && len(val) < o.Min {
		v.add(field, msgAtLeast(o.Min), "string_too_short")
	}
	if o.Max >= 0 && len(val) > o.Max {
		v.add(field, msgAtMost(o.Max), "string_too_long")
	}
	if o.Pattern != nil && !o.Pattern.MatchString(val) {
		v.add(field, "String should match pattern '"+o.PatternDesc+"'", "string_pattern_mismatch")
	}
	return val
}

// OneOf validates membership; returns true when invalid.
func (v *Validator) OneOf(field, val string, allowed ...string) bool {
	for _, a := range allowed {
		if val == a {
			return false
		}
	}
	v.add(field, "Input should be "+joinOr(allowed), "enum")
	return true
}

func (v *Validator) OK() bool { return len(v.errs) == 0 }

// Abort writes the 422 body and aborts; returns true when it aborted.
func (v *Validator) Abort(c *gin.Context) bool {
	if v.OK() {
		return false
	}
	detail := make([]ValidationError, len(v.errs))
	copy(detail, v.errs)
	c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"detail": detail})
	return true
}

// AbortInvalidJSON renders undecodable bodies as a single 422 item.
func AbortInvalidJSON(c *gin.Context, err error) {
	c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"detail": []ValidationError{
		{Loc: []any{"body"}, Msg: err.Error(), Type: "json_invalid"},
	}})
}

func msgAtLeast(n int) string {
	if n == 1 {
		return "String should have at least 1 character"
	}
	return "String should have at least " + strconv.Itoa(n) + " characters"
}

func msgAtMost(n int) string {
	return "String should have at most " + strconv.Itoa(n) + " characters"
}

func joinOr(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += " or "
		}
		out += "'" + s + "'"
	}
	return out
}
