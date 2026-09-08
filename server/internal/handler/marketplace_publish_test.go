package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// The token a publish test tries to smuggle into a listing. Split so this file
// does not itself contain a string a secret scanner would flag.
const publishTestToken = "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"

// withMarketplacePublishFlags turns on both gates at once.
//
// It cannot be expressed as two withFeatureFlag calls: that helper installs a
// fresh single-rule provider each time, so the second call would silently
// switch the first flag back off. Publishing depends on MarketplaceV1 as well
// as its own flag, so a test built that way would be exercising the
// prerequisite rather than the feature.
func withMarketplacePublishFlags(t *testing.T, marketplace, publish bool) {
	t.Helper()
	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.MarketplaceV1, featureflag.Rule{Default: marketplace})
	provider.Set(featureflags.MarketplacePublishV1, featureflag.Rule{Default: publish})
	flags := featureflag.NewService(provider)

	orig := testHandler.FeatureFlags
	testHandler.FeatureFlags = flags
	var origTask *featureflag.Service
	if testHandler.TaskService != nil {
		origTask = testHandler.TaskService.FeatureFlags
		testHandler.TaskService.FeatureFlags = flags
	}
	t.Cleanup(func() {
		testHandler.FeatureFlags = orig
		if testHandler.TaskService != nil {
			testHandler.TaskService.FeatureFlags = origTask
		}
	})
}

// uniquePublishName keeps listings from colliding across runs. The name space
// is global (D3-A) and withdrawal keeps a name reserved forever, so a fixed
// literal would pass once and conflict on every rerun.
func uniquePublishName(t *testing.T, prefix string) string {
	t.Helper()
	var suffix string
	dbfx.QueryRow(t, `SELECT replace(gen_random_uuid()::text, '-', '')`).Scan(&suffix)
	return prefix + "_" + suffix[:12]
}

// cleanupListing deletes a row outright. Withdrawal deliberately keeps the
// tombstone, so tests have to delete or they permanently reserve names in a
// shared database.
func cleanupListing(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM marketplace_listing WHERE id = $1`, id)
	})
}

func skillPublishBody(name string) MarketplacePublishRequest {
	return MarketplacePublishRequest{
		Kind:        service.MarketplaceKindSkill,
		Name:        name,
		Summary:     "A skill published by this workspace.",
		Description: "Longer prose about what it does.",
		HomepageURL: "https://example.invalid/home",
		Categories:  []string{"documents"},
		SourceURL:   "https://github.com/example/some-skill",
	}
}

func mcpPublishBody(name, transport string) MarketplacePublishRequest {
	req := MarketplacePublishRequest{
		Kind:        service.MarketplaceKindMcp,
		Name:        name,
		Summary:     "An MCP server published by this workspace.",
		Description: "Longer prose about what it does.",
		Categories:  []string{"data"},
	}
	switch transport {
	case "stdio":
		req.ConfigTemplate = json.RawMessage(`{"type":"stdio","command":"npx","args":["-y","srv","${root_path}"]}`)
		req.Placeholders = []MarketplacePlaceholderInput{{Key: "root_path", Label: "Root", Required: true}}
	case "http":
		req.ConfigTemplate = json.RawMessage(`{"type":"http","url":"https://api.example.invalid/mcp","headers":{"Authorization":"${api_token}"}}`)
		req.Placeholders = []MarketplacePlaceholderInput{{Key: "api_token", Label: "API token", Secret: true, Required: true}}
	case "sse":
		req.ConfigTemplate = json.RawMessage(`{"type":"sse","url":"https://api.example.invalid/sse","headers":{"Authorization":"${api_token}"}}`)
		req.Placeholders = []MarketplacePlaceholderInput{{Key: "api_token", Label: "API token", Secret: true, Required: true}}
	}
	return req
}

// ---- request helpers ------------------------------------------------------
//
// These deliberately do NOT set the flags the way listMarketplaceForTest does:
// a test here usually needs both flags in a specific combination, and a helper
// that reset them would undo that.

func publishListingForTest(t *testing.T, body any, mutate func(*http.Request)) (int, MarketplaceListingResponse, string) {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/marketplace/listings", body)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.PublishMarketplaceListing(w, req)
	raw := w.Body.String()

	var resp MarketplaceListingResponse
	if w.Code == http.StatusCreated || w.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode publish response: %v (%s)", err, raw)
		}
		cleanupListing(t, resp.ID)
	}
	return w.Code, resp, raw
}

func updateListingForTest(t *testing.T, id string, body any, mutate func(*http.Request)) (int, MarketplaceListingResponse, string) {
	t.Helper()
	req := newRequest(http.MethodPatch, "/api/marketplace/listings/"+id, body)
	req = withURLParams(req, "id", id)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.UpdateMarketplaceListing(w, req)
	raw := w.Body.String()

	var resp MarketplaceListingResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode update response: %v (%s)", err, raw)
		}
	}
	return w.Code, resp, raw
}

func withdrawListingForTest(t *testing.T, id string, revision int32, mutate func(*http.Request)) (int, MarketplaceListingResponse, string) {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/marketplace/listings/"+id+"/withdraw",
		MarketplacePublishRequest{Revision: revision})
	req = withURLParams(req, "id", id)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.WithdrawMarketplaceListing(w, req)
	raw := w.Body.String()

	var resp MarketplaceListingResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode withdraw response: %v (%s)", err, raw)
		}
	}
	return w.Code, resp, raw
}

func listOwnListingsForTest(t *testing.T, mutate func(*http.Request)) (int, []MarketplaceListingResponse, string) {
	t.Helper()
	req := newRequest(http.MethodGet, "/api/marketplace/listings", nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.ListMarketplaceListings(w, req)
	raw := w.Body.String()

	var resp []MarketplaceListingResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode listings: %v (%s)", err, raw)
		}
	}
	return w.Code, resp, raw
}

// listCatalogForTest reads the merged catalog without touching the flags.
func listCatalogForTest(t *testing.T, query string, mutate func(*http.Request)) (int, []MarketplaceItemResponse, string) {
	t.Helper()
	path := "/api/marketplace/items"
	if query != "" {
		path += "?" + query
	}
	req := newRequest(http.MethodGet, path, nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.ListMarketplaceItems(w, req)
	raw := w.Body.String()

	var resp []MarketplaceItemResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode catalog: %v (%s)", err, raw)
		}
	}
	return w.Code, resp, raw
}

// ---------------------------------------------------------------------------
// The happy path, per kind and per transport.
// ---------------------------------------------------------------------------

func TestPublishMarketplaceListing_Skill(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "skill")
	code, resp, raw := publishListingForTest(t, skillPublishBody(name), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d, want 201 (%s)", code, raw)
	}
	if resp.Kind != service.MarketplaceKindSkill || resp.Name != name {
		t.Fatalf("unexpected listing %+v", resp)
	}
	if resp.State != "published" {
		t.Errorf("state = %q, want published", resp.State)
	}
	if resp.Revision != 1 {
		t.Errorf("revision = %d, want 1", resp.Revision)
	}
	if resp.PublishedAt == "" {
		t.Error("a published listing must carry published_at")
	}
	// Attribution is the workspace, never the individual: the listing is
	// readable by every workspace, so a user's name here would publish org
	// membership as a side effect of publishing a skill.
	if resp.PublisherDisplayName == "" {
		t.Error("a published listing must carry workspace attribution")
	}
}

// Each transport must survive publication with its semantics intact. `sse` in
// particular must not be folded into `http`: the runtime dials it differently,
// so a rewrite at publish time would silently change what installers run.
func TestPublishMarketplaceListing_McpTransportsRoundTrip(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	for _, transport := range []string{"stdio", "http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			name := uniquePublishName(t, "mcp"+transport)
			code, resp, raw := publishListingForTest(t, mcpPublishBody(name, transport), nil)
			if code != http.StatusCreated {
				t.Fatalf("publish = %d, want 201 (%s)", code, raw)
			}
			if resp.Transport != transport {
				t.Errorf("transport = %q, want %q", resp.Transport, transport)
			}
			var stored map[string]any
			if err := json.Unmarshal(resp.ConfigTemplate, &stored); err != nil {
				t.Fatalf("stored template is not an object: %v", err)
			}
			if stored["type"] != transport {
				t.Errorf("stored type = %v, want %q", stored["type"], transport)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Secret handling — the constraint the whole feature is built around.
// ---------------------------------------------------------------------------

func TestPublishMarketplaceListing_BlocksSecretAndNeverEchoesIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	body := skillPublishBody(uniquePublishName(t, "leaky"))
	body.Description = "Set this up with:\nexport GITHUB_TOKEN=" + publishTestToken

	code, _, raw := publishListingForTest(t, body, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("publish = %d, want 422 (%s)", code, raw)
	}
	if strings.Contains(raw, publishTestToken) {
		t.Fatal("the rejection body echoed the submitted token")
	}

	var scanErr MarketplaceScanErrorResponse
	if err := json.Unmarshal([]byte(raw), &scanErr); err != nil {
		t.Fatalf("decode scan error: %v (%s)", err, raw)
	}
	if len(scanErr.Findings) == 0 {
		t.Fatal("a blocked publish must tell the publisher where to look")
	}
	if scanErr.ScannerRevision == "" {
		t.Error("the scanner revision must travel with the result")
	}
	for _, f := range scanErr.Findings {
		if f.Field == "" {
			t.Errorf("finding %+v does not name the offending field", f)
		}
	}
}

// A literal credential under a credential-named key must block even when it
// matches no shape detector: an internal token can look like an ordinary word.
func TestPublishMarketplaceListing_BlocksLiteralCredentialInTemplate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	body := mcpPublishBody(uniquePublishName(t, "literal"), "http")
	body.ConfigTemplate = json.RawMessage(`{"type":"http","url":"https://api.example.invalid/mcp","headers":{"Authorization":"hunter2"}}`)
	body.Placeholders = nil

	code, _, raw := publishListingForTest(t, body, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("publish = %d, want 422 (%s)", code, raw)
	}
	if strings.Contains(raw, "hunter2") {
		t.Fatal("the rejection body echoed the submitted credential")
	}
}

// The catalog every workspace reads must carry the placeholders an install
// needs and nothing that could hold a value.
func TestPublishedListingsNeverExposeSecretsInTheCatalog(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "catalogsafe")
	code, resp, raw := publishListingForTest(t, mcpPublishBody(name, "http"), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d, want 201 (%s)", code, raw)
	}

	listCode, items, listRaw := listCatalogForTest(t, "kind=mcp", nil)
	if listCode != http.StatusOK {
		t.Fatalf("list = %d (%s)", listCode, listRaw)
	}
	// The catalog entry carries placeholders, never the template that would
	// show where a rendered credential goes.
	if strings.Contains(listRaw, "Authorization") {
		t.Fatal("the merged catalog leaked the MCP template")
	}
	var found *MarketplaceItemResponse
	for i := range items {
		if items[i].Key == resp.Key {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatal("a published listing must appear in the merged catalog")
	}
	if len(found.Placeholders) != 1 || !found.Placeholders[0].Secret {
		t.Fatalf("the install dialog needs the secret placeholder, got %+v", found.Placeholders)
	}
}

// ---------------------------------------------------------------------------
// D3-A: global name uniqueness, tombstones, and who may republish.
// ---------------------------------------------------------------------------

func TestPublishMarketplaceListing_RejectsGlobalNameConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "dup")
	if code, _, raw := publishListingForTest(t, skillPublishBody(name), nil); code != http.StatusCreated {
		t.Fatalf("first publish = %d (%s)", code, raw)
	}
	// Normalisation is case-insensitive, so a different casing is the SAME
	// name and must be refused rather than creating a confusable twin.
	code, _, raw := publishListingForTest(t, skillPublishBody(strings.ToUpper(name)), nil)
	if code != http.StatusConflict {
		t.Fatalf("conflicting publish = %d, want 409 (%s)", code, raw)
	}
}

func TestPublishMarketplaceListing_RejectsStaticCatalogName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	// A published "filesystem" would give an installer two entries that create
	// the same workspace row, and the second install would collide on name.
	code, _, raw := publishListingForTest(t, mcpPublishBody("filesystem", "stdio"), nil)
	if code != http.StatusConflict {
		t.Fatalf("publish over a built-in = %d, want 409 (%s)", code, raw)
	}
}

func TestWithdrawnListingKeepsItsNameReservedAndOnlyItsOwnerMayRepublish(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "tomb")
	code, created, raw := publishListingForTest(t, skillPublishBody(name), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	wCode, withdrawn, wRaw := withdrawListingForTest(t, created.ID, created.Revision, nil)
	if wCode != http.StatusOK {
		t.Fatalf("withdraw = %d, want 200 (%s)", wCode, wRaw)
	}
	if withdrawn.State != "withdrawn" {
		t.Fatalf("state = %q, want withdrawn", withdrawn.State)
	}

	// The tombstone must have left the catalog.
	_, items, _ := listCatalogForTest(t, "", nil)
	for _, item := range items {
		if item.Key == created.Key {
			t.Fatal("a withdrawn listing is still discoverable")
		}
	}

	// A DIFFERENT workspace must not be able to take the freed-looking name.
	foreign := createForeignPublisherWorkspace(t)
	fCode, _, fRaw := publishListingForTest(t, skillPublishBody(name), foreign.actAs)
	if fCode != http.StatusConflict {
		t.Fatalf("foreign republish = %d, want 409 (%s)", fCode, fRaw)
	}

	// The ORIGINAL workspace brings it back, through the same publish call.
	rCode, revived, rRaw := publishListingForTest(t, skillPublishBody(name), nil)
	if rCode != http.StatusOK {
		t.Fatalf("owner republish = %d, want 200 (%s)", rCode, rRaw)
	}
	if revived.ID != created.ID {
		t.Errorf("republication should revive the same row, got %s want %s", revived.ID, created.ID)
	}
	if revived.State != "published" {
		t.Errorf("state = %q, want published", revived.State)
	}
}

func TestWithdrawMarketplaceListing_RepeatWithdrawIsRefused(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "twice")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	_, withdrawn, _ := withdrawListingForTest(t, created.ID, created.Revision, nil)
	// Absorbing the second call would re-stamp withdrawn_at and lose who
	// actually withdrew it.
	again, _, againRaw := withdrawListingForTest(t, created.ID, withdrawn.Revision, nil)
	if again != http.StatusConflict {
		t.Fatalf("second withdraw = %d, want 409 (%s)", again, againRaw)
	}
}

// A withdrawn listing must not be editable back into the catalog through the
// update path, which does not re-run the publish-time name gates.
func TestUpdateMarketplaceListing_WithdrawnListingCannotBeEdited(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "editdead")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	_, withdrawn, _ := withdrawListingForTest(t, created.ID, created.Revision, nil)

	body := skillPublishBody(created.Name)
	body.Revision = withdrawn.Revision
	uCode, _, uRaw := updateListingForTest(t, created.ID, body, nil)
	if uCode != http.StatusConflict {
		t.Fatalf("update of a tombstone = %d, want 409 (%s)", uCode, uRaw)
	}
}

// ---------------------------------------------------------------------------
// Revision: optimistic concurrency on update and withdraw.
// ---------------------------------------------------------------------------

func TestUpdateMarketplaceListing_StaleRevisionIsRefused(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "rev")
	code, created, raw := publishListingForTest(t, skillPublishBody(name), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}

	first := skillPublishBody(name)
	first.Summary = "First editor's summary."
	first.Revision = created.Revision
	okCode, updated, okRaw := updateListingForTest(t, created.ID, first, nil)
	if okCode != http.StatusOK {
		t.Fatalf("first update = %d, want 200 (%s)", okCode, okRaw)
	}
	if updated.Revision != created.Revision+1 {
		t.Fatalf("revision = %d, want %d", updated.Revision, created.Revision+1)
	}

	// The second editor read the row before the first wrote it.
	second := skillPublishBody(name)
	second.Summary = "Second editor's summary."
	second.Revision = created.Revision
	staleCode, _, staleRaw := updateListingForTest(t, created.ID, second, nil)
	if staleCode != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409 (%s)", staleCode, staleRaw)
	}

	// The first editor's write must have survived intact.
	_, listings, _ := listOwnListingsForTest(t, nil)
	for _, l := range listings {
		if l.ID == created.ID && l.Summary != "First editor's summary." {
			t.Fatalf("the losing write clobbered the winner: %q", l.Summary)
		}
	}
}

func TestWithdrawMarketplaceListing_StaleRevisionIsRefused(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "wrev")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	body := skillPublishBody(created.Name)
	body.Revision = created.Revision
	if c, _, r := updateListingForTest(t, created.ID, body, nil); c != http.StatusOK {
		t.Fatalf("update = %d (%s)", c, r)
	}
	stale, _, staleRaw := withdrawListingForTest(t, created.ID, created.Revision, nil)
	if stale != http.StatusConflict {
		t.Fatalf("stale withdraw = %d, want 409 (%s)", stale, staleRaw)
	}
}

func TestMarketplaceListingMutationsRequireARevision(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "norev")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	// A missing revision must not be read as "whatever is current": that would
	// turn every client that forgets the field into a last-write-wins client.
	body := skillPublishBody(created.Name)
	if c, _, r := updateListingForTest(t, created.ID, body, nil); c != http.StatusBadRequest {
		t.Fatalf("update without a revision = %d, want 400 (%s)", c, r)
	}
	if c, _, r := withdrawListingForTest(t, created.ID, 0, nil); c != http.StatusBadRequest {
		t.Fatalf("withdraw without a revision = %d, want 400 (%s)", c, r)
	}
}

// A rejected update must leave the row exactly as it was — no partial write of
// the fields validated before the one that failed.
func TestUpdateMarketplaceListing_RejectedUpdateRollsBack(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "rollback")
	code, created, raw := publishListingForTest(t, skillPublishBody(name), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}

	bad := skillPublishBody(name)
	bad.Summary = "This edit is fine."
	bad.Description = "But this line is not:\nAUTH_TOKEN=" + publishTestToken
	bad.Revision = created.Revision
	if c, _, r := updateListingForTest(t, created.ID, bad, nil); c != http.StatusUnprocessableEntity {
		t.Fatalf("blocked update = %d, want 422 (%s)", c, r)
	}

	_, listings, listRaw := listOwnListingsForTest(t, nil)
	if strings.Contains(listRaw, publishTestToken) {
		t.Fatal("a rejected update stored the submitted token")
	}
	seen := false
	for _, l := range listings {
		if l.ID != created.ID {
			continue
		}
		seen = true
		if l.Revision != created.Revision {
			t.Errorf("revision advanced on a rejected update: %d", l.Revision)
		}
		if l.Summary == "This edit is fine." {
			t.Error("a rejected update partially applied its accepted fields")
		}
	}
	if !seen {
		t.Fatal("the listing disappeared after a rejected update")
	}
}

// ---------------------------------------------------------------------------
// Authorisation and the flags.
// ---------------------------------------------------------------------------

func TestPublishMarketplaceListing_RequiresItsOwnFlag(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Discovery on, publishing off. Keeping the read half usable while the
	// write half is closed is the whole reason D5-A asked for a second flag.
	withMarketplacePublishFlags(t, true, false)

	code, _, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "flag")), nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("publish with the flag off = %d, want 503 (%s)", code, raw)
	}
	if c, _, r := listOwnListingsForTest(t, nil); c != http.StatusServiceUnavailable {
		t.Fatalf("management list with the flag off = %d, want 503 (%s)", c, r)
	}
	if c, _, r := listCatalogForTest(t, "", nil); c != http.StatusOK {
		t.Fatalf("closing publishing must not close discovery, got %d (%s)", c, r)
	}
}

func TestPublishMarketplaceListing_RequiresTheMarketplaceFlag(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Publishing into a marketplace nobody can browse would create listings
	// with no way to see or withdraw them.
	withMarketplacePublishFlags(t, false, true)

	code, _, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "noshop")), nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("publish = %d, want 503 (%s)", code, raw)
	}
}

func TestPublishMarketplaceListing_RejectsAgentActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	caller := createHandlerTestAgent(t, "marketplace-publisher-agent", nil)
	taskID := insertHandlerTestTask(t, caller)

	// Publishing puts a workspace's name on content every other workspace can
	// install. An autonomous agent doing that is not something anyone approved.
	code, _, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "byagent")), func(req *http.Request) {
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", caller)
		req.Header.Set("X-Task-ID", taskID)
	})
	if code != http.StatusForbidden {
		t.Fatalf("agent publish = %d, want 403 (%s)", code, raw)
	}
}

func TestPublishMarketplaceListing_RejectsNonAdminMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	var previousRole string
	dbfx.QueryRow(t, `SELECT role FROM member WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID).Scan(&previousRole)
	dbfx.Exec(t, `UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE member SET role = $1 WHERE workspace_id = $2 AND user_id = $3`,
			previousRole, testWorkspaceID, testUserID)
	})

	code, _, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "bymember")), nil)
	if code != http.StatusForbidden {
		t.Fatalf("member publish = %d, want 403 (%s)", code, raw)
	}
}

// Another workspace's listing must read as absent, not as forbidden: a 403
// would turn these endpoints into an oracle for probing listing ids.
func TestMutatingAnotherWorkspacesListingIsNotFound(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "foreignedit")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	foreign := createForeignPublisherWorkspace(t)

	body := skillPublishBody(created.Name)
	body.Revision = created.Revision
	if c, _, r := updateListingForTest(t, created.ID, body, foreign.actAs); c != http.StatusNotFound {
		t.Fatalf("foreign update = %d, want 404 (%s)", c, r)
	}
	if c, _, r := withdrawListingForTest(t, created.ID, created.Revision, foreign.actAs); c != http.StatusNotFound {
		t.Fatalf("foreign withdraw = %d, want 404 (%s)", c, r)
	}

	// And the listing must be untouched.
	_, listings, _ := listOwnListingsForTest(t, nil)
	for _, l := range listings {
		if l.ID == created.ID && (l.State != "published" || l.Revision != created.Revision) {
			t.Fatalf("a refused foreign mutation still changed the row: %+v", l)
		}
	}
}

func TestListMarketplaceListings_DoesNotShowOtherWorkspacesPublications(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "notyours")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	foreign := createForeignPublisherWorkspace(t)

	listCode, listings, listRaw := listOwnListingsForTest(t, foreign.actAs)
	if listCode != http.StatusOK {
		t.Fatalf("foreign list = %d (%s)", listCode, listRaw)
	}
	for _, l := range listings {
		if l.ID == created.ID {
			t.Fatal("the management view showed another workspace's listing")
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-workspace install, and D4-A: withdrawal leaves installed copies alone.
// ---------------------------------------------------------------------------

func TestPublishedListingIsInstallableFromAnotherWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	name := uniquePublishName(t, "xws")
	code, created, raw := publishListingForTest(t, mcpPublishBody(name, "http"), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	foreign := createForeignPublisherWorkspace(t)

	// D1-A: every published listing is visible platform-wide.
	listCode, items, listRaw := listCatalogForTest(t, "kind=mcp", foreign.actAs)
	if listCode != http.StatusOK {
		t.Fatalf("foreign list = %d (%s)", listCode, listRaw)
	}
	seen := false
	for _, item := range items {
		if item.Key == created.Key {
			seen = true
		}
	}
	if !seen {
		t.Fatal("a published listing must be discoverable from another workspace")
	}

	// Installing supplies the installing workspace's OWN secret, which must not
	// be echoed back.
	installReq := newRequest(http.MethodPost, "/api/marketplace/install", MarketplaceInstallRequest{
		Key:    created.Key,
		Name:   name,
		Values: map[string]string{"api_token": publishTestToken},
	})
	foreign.actAs(installReq)
	iw := httptest.NewRecorder()
	testHandler.InstallMarketplaceItem(iw, installReq)
	installRaw := iw.Body.String()
	if iw.Code != http.StatusCreated {
		t.Fatalf("foreign install = %d, want 201 (%s)", iw.Code, installRaw)
	}
	if strings.Contains(installRaw, publishTestToken) {
		t.Fatal("the install response echoed the supplied secret")
	}
	var installed WorkspaceMcpServerResponse
	if err := json.Unmarshal([]byte(installRaw), &installed); err != nil {
		t.Fatalf("decode install response: %v (%s)", err, installRaw)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_mcp_server WHERE id = $1`, installed.ID)
	})
	if installed.Transport != "http" {
		t.Errorf("installed transport = %q, want http", installed.Transport)
	}

	// The rendered entry landed in the installing workspace, not the publisher's.
	var storedWorkspace string
	dbfx.QueryRow(t, `SELECT workspace_id::text FROM workspace_mcp_server WHERE id = $1`, installed.ID).Scan(&storedWorkspace)
	if storedWorkspace != foreign.workspaceID {
		t.Errorf("installed row landed in workspace %s, want %s", storedWorkspace, foreign.workspaceID)
	}

	// D4-A: withdrawing the listing must not touch the installed copy.
	if wc, _, wr := withdrawListingForTest(t, created.ID, created.Revision, nil); wc != http.StatusOK {
		t.Fatalf("withdraw = %d (%s)", wc, wr)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM workspace_mcp_server WHERE id = $1`, installed.ID); n != 1 {
		t.Fatal("withdrawal removed an already-installed copy (violates D4-A)")
	}

	// But it is no longer installable.
	againReq := newRequest(http.MethodPost, "/api/marketplace/install", MarketplaceInstallRequest{
		Key:    created.Key,
		Name:   name + "2",
		Values: map[string]string{"api_token": publishTestToken},
	})
	foreign.actAs(againReq)
	aw := httptest.NewRecorder()
	testHandler.InstallMarketplaceItem(aw, againReq)
	if aw.Code != http.StatusNotFound {
		t.Fatalf("install of a withdrawn listing = %d, want 404 (%s)", aw.Code, aw.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Validation.
// ---------------------------------------------------------------------------

func TestPublishMarketplaceListing_Validation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	cases := []struct {
		name string
		body func() MarketplacePublishRequest
	}{
		{"empty name", func() MarketplacePublishRequest {
			return skillPublishBody("")
		}},
		{"name with spaces", func() MarketplacePublishRequest {
			return skillPublishBody("not a valid name")
		}},
		{"skill without a source", func() MarketplacePublishRequest {
			b := skillPublishBody(uniquePublishName(t, "nosrc"))
			b.SourceURL = ""
			return b
		}},
		{"skill with a non-http source", func() MarketplacePublishRequest {
			b := skillPublishBody(uniquePublishName(t, "filesrc"))
			b.SourceURL = "file:///etc/passwd"
			return b
		}},
		{"skill whose source embeds credentials", func() MarketplacePublishRequest {
			b := skillPublishBody(uniquePublishName(t, "credsrc"))
			b.SourceURL = "https://user:pw@example.invalid/x"
			return b
		}},
		{"mcp without a template", func() MarketplacePublishRequest {
			b := mcpPublishBody(uniquePublishName(t, "notpl"), "stdio")
			b.ConfigTemplate = nil
			b.Placeholders = nil
			return b
		}},
		{"mcp with an unknown transport", func() MarketplacePublishRequest {
			b := mcpPublishBody(uniquePublishName(t, "badtransport"), "stdio")
			b.ConfigTemplate = json.RawMessage(`{"type":"carrier-pigeon","command":"npx"}`)
			b.Placeholders = nil
			return b
		}},
		{"mcp declaring a placeholder the template never uses", func() MarketplacePublishRequest {
			b := mcpPublishBody(uniquePublishName(t, "unusedph"), "stdio")
			b.Placeholders = append(b.Placeholders, MarketplacePlaceholderInput{Key: "unused", Label: "Unused"})
			return b
		}},
		{"unknown kind", func() MarketplacePublishRequest {
			b := skillPublishBody(uniquePublishName(t, "badkind"))
			b.Kind = "plugin"
			return b
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, raw := publishListingForTest(t, tc.body(), nil)
			if code != http.StatusBadRequest {
				t.Fatalf("publish = %d, want 400 (%s)", code, raw)
			}
		})
	}
}

// An undeclared `${key}` has no input in the install dialog, so rendering would
// fail for every installer. Catching it at publish time is the difference
// between the publisher fixing it and everyone else hitting a dead listing.
func TestPublishMarketplaceListing_RejectsUndeclaredPlaceholder(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	body := mcpPublishBody(uniquePublishName(t, "undecl"), "stdio")
	body.ConfigTemplate = json.RawMessage(`{"type":"stdio","command":"npx","args":["-y","srv","${root_path}","${region}"]}`)

	code, _, raw := publishListingForTest(t, body, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("publish = %d, want 422 (%s)", code, raw)
	}
}

// ---------------------------------------------------------------------------
// The management view.
// ---------------------------------------------------------------------------

func TestListMarketplaceListings_ShowsOwnPublicationsIncludingTombstones(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplacePublishFlags(t, true, true)

	code, created, raw := publishListingForTest(t, skillPublishBody(uniquePublishName(t, "mine")), nil)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d (%s)", code, raw)
	}
	if wc, _, wr := withdrawListingForTest(t, created.ID, created.Revision, nil); wc != http.StatusOK {
		t.Fatalf("withdraw = %d (%s)", wc, wr)
	}

	listCode, listings, listRaw := listOwnListingsForTest(t, nil)
	if listCode != http.StatusOK {
		t.Fatalf("list = %d (%s)", listCode, listRaw)
	}
	found := false
	for _, l := range listings {
		if l.ID == created.ID {
			found = true
			if l.State != "withdrawn" {
				t.Errorf("state = %q, want withdrawn", l.State)
			}
		}
	}
	if !found {
		// The tombstone is what a republication acts on, so hiding it would
		// make the name it still reserves invisible to its only owner.
		t.Fatal("the management view must keep showing a withdrawn listing")
	}
	// The authority column is internal: exposing it would tell every publisher
	// which workspace owns which listing.
	if strings.Contains(listRaw, "source_workspace_id") {
		t.Fatal("the management view leaked source_workspace_id")
	}
}

// ---------------------------------------------------------------------------
// Fixtures.
// ---------------------------------------------------------------------------

type foreignPublisher struct {
	workspaceID string
	userID      string
	// actAs rewrites a request to come from this workspace's owner.
	actAs func(*http.Request)
}

// createForeignPublisherWorkspace makes a second workspace with its own owner.
// Both "another workspace can install this" and "another workspace cannot edit
// this" need one.
func createForeignPublisherWorkspace(t *testing.T) foreignPublisher {
	t.Helper()

	var suffix string
	dbfx.QueryRow(t, `SELECT replace(gen_random_uuid()::text, '-', '')`).Scan(&suffix)
	slug := "mp-foreign-" + suffix[:8]

	userID := dbfx.User(t, "Foreign Publisher", slug+"@multica.test")
	workspaceID := dbfx.Workspace(t, "Foreign Publisher WS", slug)
	dbfx.Member(t, workspaceID, userID, "owner")

	// Rows the HANDLERS create in this workspace are not the fixture's to
	// delete, so they need their own teardown — registered after the workspace
	// so it runs before it.
	dbfx.Cleanup(t, `DELETE FROM marketplace_listing WHERE source_workspace_id = $1`, workspaceID)
	dbfx.Cleanup(t, `DELETE FROM workspace_mcp_server WHERE workspace_id = $1`, workspaceID)

	return foreignPublisher{
		workspaceID: workspaceID,
		userID:      userID,
		actAs: func(req *http.Request) {
			req.Header.Set("X-User-ID", userID)
			req.Header.Set("X-Workspace-ID", workspaceID)
		},
	}
}
