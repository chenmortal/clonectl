package redis

import (
	"testing"

	"clonectl/internal/agent"
)

func TestValidateEndpoint_Happy(t *testing.T) {
	cases := []struct {
		name string
		ep   agent.Endpoint
	}{
		{"standalone", agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}}},
		{"cluster", agent.Endpoint{Mode: ModeCluster, Addresses: []string{"10.0.0.1:6379", "10.0.0.2:6379", "10.0.0.3:6379"}}},
		{"sentinel", agent.Endpoint{Mode: ModeSentinel, MasterName: "m", Addresses: []string{"10.0.0.1:26379"}}},
		{"proxy", agent.Endpoint{Mode: ModeProxy, Addresses: []string{"10.0.0.1:6379"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateEndpoint("test", tc.ep); err != nil {
				t.Errorf("unexpected: %v", err)
			}
		})
	}
}

func TestValidateEndpoint_RejectsBadTopology(t *testing.T) {
	ep := agent.Endpoint{Mode: "made_up_mode", Addresses: []string{"10.0.0.1:6379"}}
	if err := validateEndpoint("test", ep); err == nil {
		t.Fatalf("expected error for bad topology")
	}
}

func TestValidateEndpoint_RejectsMissingAddresses(t *testing.T) {
	ep := agent.Endpoint{Mode: ModeStandalone}
	if err := validateEndpoint("test", ep); err == nil {
		t.Fatalf("expected error for empty addresses")
	}
}

func TestValidateEndpoint_RejectsEmptyHostPort(t *testing.T) {
	cases := []string{"", ":6379", "10.0.0.1:", "10.0.0.1"}
	for _, bad := range cases {
		ep := agent.Endpoint{Mode: ModeStandalone, Addresses: []string{bad}}
		if err := validateEndpoint("test", ep); err == nil {
			t.Errorf("expected error for address %q", bad)
		}
	}
}

func TestValidateEndpoint_RequiresMasterNameForSentinel(t *testing.T) {
	ep := agent.Endpoint{Mode: ModeSentinel, Addresses: []string{"10.0.0.1:26379"}}
	if err := validateEndpoint("test", ep); err == nil {
		t.Fatalf("expected error: sentinel without master_name")
	}
}

func TestSplitHostPort_IPv6(t *testing.T) {
	host, port, err := splitHostPort("[::1]:6379")
	if err != nil {
		t.Fatalf("splitHostPort: %v", err)
	}
	if host != "::1" || port != "6379" {
		t.Errorf("got host=%s port=%s, want ::1/6379", host, port)
	}
}

func TestSpecToShakePayload_IncludesAddressAndUsername(t *testing.T) {
	spec := agent.Spec{
		Mode: "sync_reader",
		Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}, Username: "src"},
		Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}, Username: "dst"},
	}
	raw, err := specToShakePayload(spec)
	if err != nil {
		t.Fatalf("specToShakePayload: %v", err)
	}
	s := string(raw)
	for _, want := range []string{`"address":"10.0.0.1:6379"`, `"address":"10.0.0.2:6379"`, `"username":"src"`, `"username":"dst"`, `"mode":"redis_writer"`} {
		if !contains(s, want) {
			t.Errorf("payload missing %s\nfull: %s", want, s)
		}
	}
}

func TestSpecToShakePayload_RejectsBadMode(t *testing.T) {
	spec := agent.Spec{Mode: "made_up_mode"}
	if _, err := specToShakePayload(spec); err == nil {
		t.Fatalf("expected error for bad mode")
	}
}

func TestSpecToFullCheckPayload_AllFourModes(t *testing.T) {
	for _, mode := range []string{"1", "2", "3", "4"} {
		spec := agent.Spec{
			Mode:   mode,
			Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
			Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
		}
		if _, err := specToFullCheckPayload(spec); err != nil {
			t.Errorf("mode %s: %v", mode, err)
		}
	}
}

func TestSpecToFullCheckPayload_RejectsBadMode(t *testing.T) {
	spec := agent.Spec{
		Mode:   "9",
		Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
		Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
	}
	if _, err := specToFullCheckPayload(spec); err == nil {
		t.Fatalf("expected error for compare_mode 9")
	}
}

// local contains helper to avoid importing strings just for this.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
