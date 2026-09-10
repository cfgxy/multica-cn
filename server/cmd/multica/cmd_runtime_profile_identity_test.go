package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestValidateProtocolFamilyAcceptsIndependentRuntimeFamilies is the CLI half
// of the identity split: deerflow and zcode are protocol families of their
// own, so `multica runtime-profile create --protocol-family deerflow` must no
// longer be a client-side typo. The CLI names of the bridges must keep
// failing: they are executables a profile launches, not protocol identities,
// and the server and the runtime_profile CHECK both reject them.
func TestValidateProtocolFamilyAcceptsIndependentRuntimeFamilies(t *testing.T) {
	for _, family := range []string{"deerflow", "zcode", "kimi"} {
		if err := validateProtocolFamily(family); err != nil {
			t.Errorf("validateProtocolFamily(%q) = %v, want nil", family, err)
		}
	}

	for _, family := range []string{"deerflow-acp", "zcode-acp"} {
		err := validateProtocolFamily(family)
		if err == nil {
			t.Errorf("validateProtocolFamily(%q) = nil, want a rejection: the bridge command name is not a protocol family", family)
			continue
		}
		// The message lists the whitelist so an operator can correct the flag
		// without reading the server source.
		if !strings.Contains(err.Error(), "deerflow") || !strings.Contains(err.Error(), "zcode") {
			t.Errorf("validateProtocolFamily(%q) error should list the accepted families, got: %v", family, err)
		}
	}
}

// TestValidateProtocolFamilyTracksSupportedTypes keeps the CLI gate from
// drifting into its own private list. It derives from agent.SupportedTypes on
// purpose: a family the server accepts but the CLI rejects would be
// unreachable from the command line for no reason a user could diagnose.
func TestValidateProtocolFamilyTracksSupportedTypes(t *testing.T) {
	for _, family := range agent.SupportedTypes {
		if err := validateProtocolFamily(family); err != nil {
			t.Errorf("validateProtocolFamily(%q) rejected a supported type: %v", family, err)
		}
	}
}

// TestRunRuntimeProfileCreateSendsIndependentFamilyVerbatim checks the value
// actually put on the wire. Before the split, reaching either bridge meant
// declaring `kimi` and letting the command name decide the backend; the family
// must now travel to the server unchanged so the daemon registers the runtime
// under its own identity.
func TestRunRuntimeProfileCreateSendsIndependentFamilyVerbatim(t *testing.T) {
	cases := []struct {
		family  string
		command string
	}{
		{family: "deerflow", command: "deerflow-acp"},
		{family: "zcode", command: "zcode-acp"},
	}
	for _, tc := range cases {
		t.Run(tc.family, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("MULTICA_TOKEN", "test-token")
			t.Setenv("MULTICA_WORKSPACE_ID", "ws-123")

			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "prof-1"})
			}))
			defer srv.Close()
			t.Setenv("MULTICA_SERVER_URL", srv.URL)

			cmd := newProfileCreateTestCmd()
			_ = cmd.Flags().Set("protocol-family", tc.family)
			_ = cmd.Flags().Set("command-name", tc.command)
			_ = cmd.Flags().Set("display-name", "Identity "+tc.family)

			if err := runRuntimeProfileCreate(cmd, nil); err != nil {
				t.Fatalf("runRuntimeProfileCreate: %v", err)
			}
			if gotBody["protocol_family"] != tc.family {
				t.Errorf("protocol_family = %#v, want %q", gotBody["protocol_family"], tc.family)
			}
			if gotBody["command_name"] != tc.command {
				t.Errorf("command_name = %#v, want %q", gotBody["command_name"], tc.command)
			}
		})
	}
}
