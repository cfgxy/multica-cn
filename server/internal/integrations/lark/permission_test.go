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
	if len(catalog) != 7 {
		t.Fatalf("expected 7 capabilities, got %d", len(catalog))
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
		CapabilityDriveFileLinks, CapabilityWikiDocLinks,
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

// TestCapabilityCatalogShareLinkRows pins the RUYI-572 rows' exact
// AND-of-OR shape: drive_file_links is one OR group of four drive read
// scopes (any one grants the download endpoint, ADR 005 §3 直抓);
// wiki_doc_links is the AND of the wiki routing group and the docx read
// group — get_node resolves the wiki token but raw_content still needs a
// docx read scope, so an install holding only one side must read as
// missing.
func TestCapabilityCatalogShareLinkRows(t *testing.T) {
	t.Parallel()
	drive, ok := CapabilitySpecByID(CapabilityDriveFileLinks)
	if !ok {
		t.Fatal("drive_file_links missing from catalog")
	}
	if !drive.Probeable {
		t.Error("drive_file_links must be probeable")
	}
	wantDrive := [][]string{
		{"drive:drive", "drive:drive:readonly", "drive:file", "drive:file:readonly"},
	}
	if !slices.EqualFunc(drive.Scopes, wantDrive, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Errorf("drive_file_links scopes = %v, want %v", drive.Scopes, wantDrive)
	}

	wiki, ok := CapabilitySpecByID(CapabilityWikiDocLinks)
	if !ok {
		t.Fatal("wiki_doc_links missing from catalog")
	}
	if !wiki.Probeable {
		t.Error("wiki_doc_links must be probeable")
	}
	wantWiki := [][]string{
		{"wiki:wiki", "wiki:wiki:readonly"},
		{"docx:document:readonly", "docx:document"},
	}
	if !slices.EqualFunc(wiki.Scopes, wantWiki, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Errorf("wiki_doc_links scopes = %v, want %v", wiki.Scopes, wantWiki)
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
		// receive_messages: one group-side scope AND one p2p-side scope —
		// the receive-event doc's full subscription set, partitioned by
		// delivery side.
		{CapabilityReceiveMessages, []string{
			"im:message.group_at_msg", "im:message.group_at_msg.include_bot:readonly", "im:message.group_at_msg:readonly",
			"im:message.group_bot_msg:readonly", "im:message.group_msg", "im:message.group_msg.include_bot:read", "im:message.group_msg:readonly",
			"im:message.p2p_msg", "im:message.p2p_msg:readonly",
		}},
		// send_messages: the POST /im/v1/messages doc's three-way grant
		// row — im:message:send is the closed-to-new-apps historical
		// scope, kept for legacy-install diff correctness.
		{CapabilitySendMessages, []string{"im:message", "im:message:send", "im:message:send_as_bot"}},
		// read_history: the intersection of the single-message GET and the
		// conversation-list requirements — im:message.history:readonly
		// grants the list but NOT the quoted-message GET, so it must not
		// satisfy this capability on its own.
		{CapabilityReadHistory, []string{
			"im:message", "im:message.group_msg", "im:message:readonly",
		}},
		// media_resources: the message-resource doc's grant list.
		{CapabilityMediaResources, []string{
			"im:message", "im:message.history:readonly", "im:message:readonly",
		}},
		// contact_lookup: union of the single-user doc's two requirement
		// rows (API gate AND name-field grants) — ScopesForCapability
		// flattens groups; dedup collapses the three shared grants.
		{CapabilityContactLookup, []string{
			"contact:contact.base:readonly", "contact:contact:access_as_app", "contact:contact:readonly", "contact:contact:readonly_as_app", "contact:user.base:readonly",
		}},
		// RUYI-572 (ADR 005 §3/§4): the two share-link capabilities.
		{CapabilityDriveFileLinks, []string{
			"drive:drive", "drive:drive:readonly", "drive:file", "drive:file:readonly",
		}},
		{CapabilityWikiDocLinks, []string{
			"docx:document", "docx:document:readonly", "wiki:wiki", "wiki:wiki:readonly",
		}},
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
	granted := []string{"im:message", "im:message:send_as_bot"}
	for _, id := range []CapabilityID{CapabilityReadHistory, CapabilitySendMessages, CapabilityMediaResources} {
		want := ScopesForCapability(id)
		got := MissingScopes(want, granted)
		switch id {
		case CapabilityReadHistory:
			want = []string{"im:message.group_msg", "im:message:readonly"}
		case CapabilityMediaResources:
			want = []string{"im:message.history:readonly", "im:message:readonly"}
		case CapabilitySendMessages:
			// The flatten-based diff lists every candidate grant, so the
			// historical im:message:send shows up even though the granted
			// im:message already satisfies the group.
			want = []string{"im:message:send"}
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
		// 230001/230027 are enforced at the business layer against a REAL
		// target ("bot is not in the chat", missing im:message.group_msg).
		// A probe carries a synthetic target, so parameter validation runs
		// before any business-layer scope check — QA live evidence
		// 2026-10-08: a send-granted install got 230001 "invalid
		// receive_id" on the send probe. Both mean the gateway's scope
		// check passed → granted.
		{"230001 param validation on synthetic target", &APIError{Op: "send message", Code: 230001, Msg: "invalid receive_id"}, ProbeGranted},
		{"230027 business-layer gap not reachable on synthetic target", &APIError{Op: "list chat messages", Code: 230027, Msg: "missing im:message.group_msg"}, ProbeGranted},
		// QA-observed not-exist family on synthetic targets (contact
		// lookup / message GET / resource download), installs verified
		// granted via the data plane → target validation, gateway passed.
		{"contact not-exist 99992351", &APIError{Op: "get user", Code: 99992351}, ProbeGranted},
		{"message id not-exist 99992354", &APIError{Op: "get message", Code: 99992354}, ProbeGranted},
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
		// Drive family (RUYI-572): 1061004 "file not exist" is the one
		// ADR-documented target-rejection code — passing it proves the
		// gateway scope check succeeded. Explicit allowlist, NOT a range
		// rule: unseen 106xxxx members (incl. business-layer permission
		// denials) must conservatively stay unknown until the QA live
		// test calibrates them (ADR 005 §4/§9).
		{"drive not-exist 1061004", &APIError{Op: "download drive file", Code: 1061004}, ProbeGranted},
		{"drive uncalibrated 1061001", &APIError{Op: "download drive file", Code: 1061001}, ProbeUnknown},
		{"drive uncalibrated 1062000", &APIError{Op: "download drive file", Code: 1062000}, ProbeUnknown},
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
	driveErr, wikiErr, docxErr             error
	// last requested synthetic targets, asserted by the probe tests
	driveToken, wikiToken, documentID string
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
func (s *probeStubClient) GetUserName(context.Context, InstallationCredentials, string) (string, error) {
	return "", s.usersErr
}
func (s *probeStubClient) DownloadDriveFile(_ context.Context, _ InstallationCredentials, fileToken string) (DownloadedResource, error) {
	s.driveToken = fileToken
	return DownloadedResource{}, s.driveErr
}
func (s *probeStubClient) GetWikiNode(_ context.Context, _ InstallationCredentials, wikiToken string) (WikiNode, error) {
	s.wikiToken = wikiToken
	return WikiNode{}, s.wikiErr
}
func (s *probeStubClient) GetDocxRawContent(_ context.Context, _ InstallationCredentials, documentID string) (string, error) {
	s.documentID = documentID
	return "", s.docxErr
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
	t.Run("contact_lookup probes the single-user lookup", func(t *testing.T) {
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
	t.Run("drive_file_links granted via synthetic-target rejection", func(t *testing.T) {
		t.Parallel()
		stub := &probeStubClient{driveErr: &APIError{Op: "download drive file", Code: 1061004}}
		out := ProbeCapability(context.Background(), stub, creds, CapabilityDriveFileLinks)
		if out.Status != ProbeGranted {
			t.Errorf("status = %q (%s), want granted", out.Status, out.Detail)
		}
		if stub.driveToken != probeDriveFileToken {
			t.Errorf("probe token = %q, want %q", stub.driveToken, probeDriveFileToken)
		}
	})
	t.Run("drive_file_links missing on permission code", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{driveErr: noPerm}, creds, CapabilityDriveFileLinks)
		if out.Status != ProbeMissing {
			t.Errorf("status = %q (%s), want missing", out.Status, out.Detail)
		}
	})
	t.Run("drive_file_links unknown on transport failure", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeStubClient{driveErr: errors.New("dial tcp: refused")}, creds, CapabilityDriveFileLinks)
		if out.Status != ProbeUnknown {
			t.Errorf("status = %q (%s), want unknown", out.Status, out.Detail)
		}
	})
	t.Run("wiki_doc_links granted only when BOTH endpoints pass", func(t *testing.T) {
		t.Parallel()
		stub := &probeStubClient{}
		out := ProbeCapability(context.Background(), stub, creds, CapabilityWikiDocLinks)
		if out.Status != ProbeGranted {
			t.Errorf("status = %q (%s), want granted", out.Status, out.Detail)
		}
		if stub.wikiToken != probeWikiToken || stub.documentID != probeDocumentID {
			t.Errorf("synthetic targets = %q / %q, want %q / %q", stub.wikiToken, stub.documentID, probeWikiToken, probeDocumentID)
		}
	})
	t.Run("wiki_doc_links missing when wiki group missing", func(t *testing.T) {
		t.Parallel()
		stub := &probeStubClient{wikiErr: noPerm}
		out := ProbeCapability(context.Background(), stub, creds, CapabilityWikiDocLinks)
		if out.Status != ProbeMissing {
			t.Errorf("status = %q (%s), want missing", out.Status, out.Detail)
		}
	})
	t.Run("wiki_doc_links missing when docx group missing", func(t *testing.T) {
		t.Parallel()
		stub := &probeStubClient{docxErr: noPerm}
		out := ProbeCapability(context.Background(), stub, creds, CapabilityWikiDocLinks)
		if out.Status != ProbeMissing {
			t.Errorf("status = %q (%s), want missing — wiki routing alone cannot grant raw_content", out.Status, out.Detail)
		}
	})
	t.Run("wiki_doc_links unknown when one probe is inconclusive", func(t *testing.T) {
		t.Parallel()
		stub := &probeStubClient{wikiErr: errors.New("dial tcp: refused")}
		out := ProbeCapability(context.Background(), stub, creds, CapabilityWikiDocLinks)
		if out.Status != ProbeUnknown {
			t.Errorf("status = %q (%s), want unknown", out.Status, out.Detail)
		}
	})
	t.Run("share-link probes degrade to unknown without a ShareLinkClient", func(t *testing.T) {
		t.Parallel()
		out := ProbeCapability(context.Background(), &probeNoShareClient{}, creds, CapabilityDriveFileLinks)
		if out.Status != ProbeUnknown || out.Detail == "" {
			t.Errorf("status = %q detail = %q, want unknown with reason", out.Status, out.Detail)
		}
	})
}

// probeNoShareClient models an APIClient predating the share-link
// surface (stub client, older fakes): the share-link probes must
// honestly report unknown instead of panicking on the type assertion.
type probeNoShareClient struct {
	APIClient
}
