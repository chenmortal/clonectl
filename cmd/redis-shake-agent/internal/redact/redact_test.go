package redact

import "testing"

func TestRedact_ReplacesPassword(t *testing.T) {
	cases := []struct {
		in       string
		wantSub  string
		notAllow []string // values that must NOT survive in the output
	}{
		{`password=hunter2`, "<redacted>", []string{"hunter2"}},
		{`PASSWORD: "hunter2"`, "<redacted>", []string{"hunter2"}},
		{`auth=secret`, "<redacted>", []string{"secret"}},
		{`token=abc.def.ghi`, "<redacted>", []string{"abc.def.ghi"}},
		{`access_key=AKIA`, "<redacted>", []string{"AKIA"}},
		{`connecting with secret=hidden`, "<redacted>", []string{"hidden"}},
	}
	for i, tc := range cases {
		got := Redact(tc.in)
		if !contains(got, tc.wantSub) {
			t.Errorf("case %d: %q -> %q, missing %q", i, tc.in, got, tc.wantSub)
		}
		for _, na := range tc.notAllow {
			if contains(got, na) {
				t.Errorf("case %d: value %q leaked through redact: %q", i, na, got)
			}
		}
	}
}

func TestRedact_NoMatch(t *testing.T) {
	// Unrelated text passes through unchanged.
	in := "connecting to redis 10.0.0.1:6379"
	if got := Redact(in); got != in {
		t.Errorf("Redact modified clean text: %q -> %q", in, got)
	}
}

func TestRedact_PreservesNonSensitiveKeyValues(t *testing.T) {
	// address=... is intentionally NOT in the redact list because we
	// need operators to see which Redis they're pointed at.
	in := `address=10.0.0.1:6379 username=foo`
	if got := Redact(in); got != in {
		t.Errorf("Redact modified non-sensitive kv: %q -> %q", in, got)
	}
}

func TestContainsSensitive(t *testing.T) {
	if !ContainsSensitive("password=foo") {
		t.Errorf("ContainsSensitive should hit")
	}
	if ContainsSensitive("hello world") {
		t.Errorf("ContainsSensitive should miss")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
