package lark

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestCapabilityCatalogShape(t *testing.T) {
	t.Parallel()
	catalog := CapabilityCatalog()
	if len(catalog) != 5 {
		t.Fatalf("expected 5 capabilities, got %d", len(catalog))
	}
	seen := map[CapabilityID]bool{}
	for _, spec := range catalog {
		if seen[spec.ID] {
			t.Errorf("duplicate capability %q", spec.ID)
		}
		seen[spec.ID] = true
		if len(spec.Scopes) == 0 {
			t.Errorf("capability %q has no scope groups", spec.ID)
		}
		for gi, group := range spec.Scopes {
			if len(group) == 0 {
				t.Errorf("capability %q group %d is empty", spec.ID, gi)
			}
		}
	}
	for _, id := range []CapabilityID{
		CapabilityReceiveMessages, CapabilitySendMessages, CapabilityReadHistory,
		CapabilityMediaResources, CapabilityContactLookup,
	} {
		if !seen[id] {
			t.Errorf("catalog missing capability %q", id)
		}
	}
	// Mutating the returned copy must not touch the source of truth.
	catalog[0].Scopes = nil
	if len(capabilityCatalog[0].Scopes) == 0 {
		t.Fatal("CapabilityCatalog returned the backing slice, not a copy")
	}
}

func TestCapabilitySpecByID(t *testing.T) {
	t.Parallel()
	if spec, ok := CapabilitySpecByID(CapabilityReadHistory); !ok || spec.ID != CapabilityReadHistory {
		t.Fatalf("CapabilitySpecByID(read_history) = %v, %v", spec, ok)
	}
	if _, ok := CapabilitySpecByID("nonexistent"); ok {
		t.Fatal("unknown capability must not resolve")
	}
}

func TestScopesForCapability(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   CapabilityID
		want []string
	}{
		{CapabilityReceiveMessages, []string{
			"im:message.group_at_msg", "im:message.group_at_msg:readonly",
			"im:message.p2p_msg", "im:message.p2p_msg:readonly",
		}},
		{CapabilitySendMessages, []string{"im:message", "im:message:send_as_bot"}},
		{CapabilityReadHistory, []string{
			"im:message", "im:message.group_msg", "im:message.history:readonly", "im:message:readonly",
		}},
		{CapabilityMediaResources, []string{"im:resource"}},
		{CapabilityContactLookup, []string{"contact:user.base:readonly"}},
	}
	for _, tc := range cases {
		got := ScopesForCapability(tc.id)
		if !slices.Equal(got, tc.want) {
			t.Errorf("ScopesForCapability(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
	if got := ScopesForCapability("nonexistent"); got != nil {
		t.Errorf("ScopesForCapability(unknown) = %v, want nil", got)
	}
}

func TestMissingScopes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		required []string
		granted  []string
		want     []string
	}{
		{"empty required", nil, []string{"a"}, nil},
		{"all granted", []string{"a", "b"}, []string{"b", "a"}, nil},
		{"none granted", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"partial", []string{"a", "b", "c"}, []string{"b"}, []string{"a", "c"}},
		{"duplicates deduped", []string{"a", "a", "b"}, []string{}, []string{"a", "b"}},
		{"result sorted", []string{"z", "a"}, []string{}, []string{"a", "z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MissingScopes(tc.required, tc.granted)
			if !slices.Equal(got, tc.want) {
				t.Errorf("MissingScopes(%v, %v) = %v, want %v", tc.required, tc.granted, got, tc.want)
			}
		})
	}
}

// TestCapabilityScopeDiffGroupMsgGap covers the real-world read_history
// diff: the app holds the base read scope but not im:message.group_msg
// (Lark rejects group history reads with code 230027), so the re-grant
// flow must surface exactly that one scope.
func TestCapabilityScopeDiffGroupMsgGap(t *testing.T) {
	t.Parallel()
	granted := []string{"im:message", "im:message:send_as_bot", "im:resource"}
	for _, id := range []CapabilityID{CapabilityReadHistory, CapabilitySendMessages, CapabilityMediaResources} {
		want := ScopesForCapability(id)
		got := MissingScopes(want, granted)
		switch id {
		case CapabilityReadHistory:
			want = []string{"im:message.group_msg", "im:message.history:readonly", "im:message:readonly"}
		default:
			want = nil
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s diff = %v, want %v", id, got, want)
		}
	}
}

func TestClassifyProbeError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want ProbeStatus
	}{
		{"nil", nil, ProbeGranted},
		{"canonical no-permission 99991672", &APIError{Op: "get message", Code: 99991672}, ProbeMissing},
		{"no-permission 99991002", &APIError{Op: "list chat messages", Code: 99991002}, ProbeMissing},
		{"no-permission 230001", &APIError{Op: "get message", Code: 230001}, ProbeMissing},
		{"group_msg gap 230027", &APIError{Op: "list chat messages", Code: 230027, Msg: "missing im:message.group_msg"}, ProbeMissing},
		{"not-exist business code", &APIError{Op: "get message", Code: 230002}, ProbeGranted},
		{"deleted 230110", &APIError{Op: "get message", Code: 230110}, ProbeGranted},
		{"invisible 230050", &APIError{Op: "get message", Code: 230050}, ProbeGranted},
		{"rate limit 230020", &APIError{Op: "send message", Code: 230020}, ProbeGranted},
		{"token invalid 99991663", &APIError{Op: "get message", Code: 99991663}, ProbeUnknown},
		{"token invalid via non-2xx", &larkAPIStatusError{StatusCode: 400, Code: 99991663}, ProbeUnknown},
		{"transport without code", errors.New("dial tcp: connection refused"), ProbeUnknown},
		{"http 502 html", &larkAPIStatusError{StatusCode: 502, Code: 0, Raw: "<html>gateway</html>"}, ProbeUnknown},
		{"wrapped APIError", fmt.Errorf("lark http client: get message: %w", &APIError{Op: "get message", Code: 99991672}), ProbeMissing},
		{"other 999xxxxx server error", &APIError{Op: "get message", Code: 99991400}, ProbeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyProbeError(tc.err); got != tc.want {
				t.Errorf("classifyProbeError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// probeStubClient fails exactly one API surface, so each test pins the
// classification of a single probe call.
type probeStubClient struct {
	APIClient
	getErr, sendErr, downloadErr, usersErr error
}

func (s *probeStubClient) GetMessage(context.Context, InstallationCredentials, string) ([]LarkMessage, error) {
	return nil, s.getErr
}
func (s *probeStubClient) SendTextMessage(context.Context, SendTextParams) (string, error) {
	return "", s.sendErr
}
func (s *probeStubClient) DownloadMessageResource(context.Context, InstallationCredentials, DownloadResourceParams) (DownloadedResource, error) {
	return DownloadedResource{}, s.downloadErr
}
func (s *probeStubClient) BatchGetUsers(context.Context, InstallationCredentials, []string) (map[string]string, error) {
	return nil, s.usersErr
}

func TestProbeCapabilityClassifiesEachSurface(t *testing.T) {
	t.Parallel()
	creds := InstallationCredentials{AppID: "cli_probe", AppSecret: "s"}
	noPerm := &APIError{Op: "get message", Code: 99991672, Msg: "no permission"}
	paramErr := &APIError{Op: "get message", Code: 230002, Msg: "invalid message id"}

	t.Run("read_history granted via synthetic-id param error", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{getErr: paramErr}, creds, CapabilityReadHistory)
		if out.Status != ProbeGranted {
			t.Errorf("status = %q (%s), want granted", out.Status, out.Detail)
		}
	})
	t.Run("read_history missing on permission code", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{getErr: noPerm}, creds, CapabilityReadHistory)
		if out.Status != ProbeMissing {
			t.Errorf("status = %q (%s), want missing", out.Status, out.Detail)
		}
	})
	t.Run("send_messages probes with synthetic chat", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{}, creds, CapabilitySendMessages)
		if out.Status != ProbeGranted {
			t.Errorf("status = %q (%s), want granted", out.Status, out.Detail)
		}
	})
	t.Run("media_resources probes download", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{}, creds, CapabilityMediaResources)
		if out.Status != ProbeGranted {
			t.Errorf("status = %q (%s), want granted", out.Status, out.Detail)
		}
	})
	t.Run("contact_lookup probes batch users", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{}, creds, CapabilityContactLookup)
		if out.Status != ProbeGranted {
			t.Errorf("status = %q (%s), want granted", out.Status, out.Detail)
		}
	})
	t.Run("receive_messages is honest unknown", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{}, creds, CapabilityReceiveMessages)
		if out.Status != ProbeUnknown || out.Detail == "" {
			t.Errorf("status = %q detail = %q, want unknown with reason", out.Status, out.Detail)
		}
	})
	t.Run("transport failure is unknown", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{usersErr: errors.New("dial tcp: refused")}, creds, CapabilityContactLookup)
		if out.Status != ProbeUnknown {
			t.Errorf("status = %q (%s), want unknown", out.Status, out.Detail)
		}
	})
}
