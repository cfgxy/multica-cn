package lark

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// CapabilityID names one runtime ability a Feishu bot installation needs.
// The catalog below is the single source of truth (RUYI-400) mapping each
// ability onto the Feishu OpenAPI scopes that grant it: the install-time
// permission panel, the post-install probe, and the 补授权 (re-grant)
// diff all read from it, so a scope change happens in exactly one place.
type CapabilityID string

const (
	// CapabilityReceiveMessages — the WS event subscription that delivers
	// @-mentions in group chats and p2p messages. It governs event push,
	// not request authz, so no REST probe can test it; its status is
	// surfaced as unknown and judged by whether messages actually arrive.
	CapabilityReceiveMessages CapabilityID = "receive_messages"
	// CapabilitySendMessages — posting replies (text / cards) into a chat.
	CapabilitySendMessages CapabilityID = "send_messages"
	// CapabilityReadHistory — GetMessage by id (quoted-reply enrichment)
	// and ListChatMessages (the recent-context window).
	CapabilityReadHistory CapabilityID = "read_history"
	// CapabilityMediaResources — downloading inbound images/files via
	// /im/v1/messages/:message_id/resources/:file_key.
	CapabilityMediaResources CapabilityID = "media_resources"
	// CapabilityContactLookup — resolving open_ids to display names via
	// /contact/v3/users/{open_id} (the CC Connect single-user lookup)
	// for speaker labels in enriched context.
	CapabilityContactLookup CapabilityID = "contact_lookup"
)

// CapabilitySpec maps one capability onto the scopes that grant it.
//
// Scopes is AND-of-OR: every inner group must be satisfied by at least
// one of its members. Feishu expresses exactly this shape on its API
// pages ("开启其中任意一项权限即可调用" per requirement row). Every entry
// below is anchored to the Feishu OpenAPI doc page of the endpoint the
// capability actually calls (open.feishu.cn, fetched 2026-10-08; full
// audit in docs/adr/004-feishu-capability-scope-mapping.md):
//
//   - receive_messages → im.message.receive_v1 event doc (its 权限要求
//     row lists nine subscription scopes; Multica needs one GROUP-side
//     scope AND one P2P-side scope to cover both delivery paths)
//   - send_messages → POST /im/v1/messages doc; its grant row is
//     three-way (im:message / im:message:send_as_bot / im:message:send)
//     — the third is the closed-to-new-apps historical scope, kept so a
//     legacy install holding only it does not falsely read as missing
//   - read_history → GET /im/v1/messages/{id} (quoted/forwarded) AND
//     GET /im/v1/messages (recent context) docs; the required set is
//     their intersection — see the catalog entry
//   - media_resources → GET /im/v1/messages/{id}/resources/{file_key} doc
//   - contact_lookup → GET /contact/v3/users/{open_id} doc (the
//     endpoint migrated from CC Connect; two requirement levels: the
//     API gate row AND the name field's grant row)
type CapabilitySpec struct {
	ID     CapabilityID
	Scopes [][]string
	// Probeable reports whether a synthetic REST call can produce an
	// honest verdict. Only the event subscription is not: it governs
	// event push, not request authz.
	Probeable bool
}

// ProbeCapabilityBudget bounds one whole capability sweep (four synthetic
// calls worst case), shared by the install-time probe and the HTTP recheck
// endpoint.
const ProbeCapabilityBudget = 10 * time.Second

// capabilityCatalog is the verified mapping, in display order.
//
// RUYI-546 calibration against the per-endpoint Feishu doc pages:
//
//   - receive_messages gains the full nine-scope subscription set from
//     the receive-event page, partitioned into GROUP-side and P2P-side
//     delivery: a bot granted only im:message.group_msg (a common
//     CC Connect-shaped install) subscribes the event and receives
//     group @-mentions, so the old group-side pair falsely read as
//     missing for it.
//   - read_history drops im:message.history:readonly from the base
//     group: that scope grants the conversation LIST but not the
//     single-message GET the quoted/forwarded enrichment needs, so an
//     install holding only it would pass the scope diff yet fail every
//     quoted reply. The required set is the two endpoints'
//     intersection; the list-only alternative stays documented here and
//     in the audit.
//   - media_resources: the message-resource page grants the endpoint to
//     im:message OR im:message:readonly OR im:message.history:readonly
//     — there is no "im:resource" requirement (that scope is not in the
//     page's 权限要求 row), so installs following the old catalog chased
//     a scope the Feishu console does not offer for this API.
//   - contact_lookup: migrated to the single-user endpoint (CC Connect
//     parity). Its doc expresses a two-level model: an API gate row
//     (contact:contact.base:readonly / access_as_app / readonly /
//     readonly_as_app) AND a name-field row (contact:user.base:readonly
//     plus the same three contact:contact grants). The old catalog
//     merged the levels into one OR group — an install holding only
//     the field-level scope would pass the diff yet fail the call.
var capabilityCatalog = []CapabilitySpec{
	{CapabilityReceiveMessages, [][]string{
		{"im:message.group_at_msg", "im:message.group_at_msg:readonly", "im:message.group_msg",
			"im:message.group_at_msg.include_bot:readonly", "im:message.group_msg.include_bot:read",
			"im:message.group_bot_msg:readonly", "im:message.group_msg:readonly"},
		{"im:message.p2p_msg", "im:message.p2p_msg:readonly"},
	}, false},
	{CapabilitySendMessages, [][]string{
		{"im:message", "im:message:send_as_bot", "im:message:send"},
	}, true},
	{CapabilityReadHistory, [][]string{
		{"im:message", "im:message:readonly"},
		{"im:message.group_msg"},
	}, true},
	{CapabilityMediaResources, [][]string{
		{"im:message", "im:message:readonly", "im:message.history:readonly"},
	}, true},
	{CapabilityContactLookup, [][]string{
		{"contact:contact.base:readonly", "contact:contact:access_as_app", "contact:contact:readonly", "contact:contact:readonly_as_app"},
		{"contact:user.base:readonly", "contact:contact:access_as_app", "contact:contact:readonly", "contact:contact:readonly_as_app"},
	}, true},
}

// CapabilityCatalog returns the catalog in display order. The slice is a
// copy, so callers cannot mutate the source of truth.
func CapabilityCatalog() []CapabilitySpec {
	out := make([]CapabilitySpec, len(capabilityCatalog))
	copy(out, capabilityCatalog)
	return out
}

// CapabilitySpecByID looks up one capability's spec.
func CapabilitySpecByID(id CapabilityID) (CapabilitySpec, bool) {
	for _, spec := range capabilityCatalog {
		if spec.ID == id {
			return spec, true
		}
	}
	return CapabilitySpec{}, false
}

// ScopesForCapability flattens the AND-of-OR requirement into the deduped,
// sorted union of scopes the Feishu console must grant ("at least one per
// group"). This is the actionable list shown for a missing capability.
func ScopesForCapability(id CapabilityID) []string {
	spec, ok := CapabilitySpecByID(id)
	if !ok {
		return nil
	}
	return MissingScopes(flatten(spec.Scopes), nil)
}

// MissingScopes returns the sorted, deduped scopes in required that are
// absent from granted — the 差集 a re-grant flow turns into "add these
// scopes in the Feishu console".
func MissingScopes(required, granted []string) []string {
	if len(required) == 0 {
		return nil
	}
	has := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		has[s] = struct{}{}
	}
	seen := make(map[string]struct{}, len(required))
	out := make([]string, 0, len(required))
	for _, s := range required {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		if _, ok := has[s]; ok {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// flatten unions the AND-of-OR groups into one deduped slice.
func flatten(groups [][]string) []string {
	var out []string
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

// ProbeStatus is the honest tri-state a capability probe can conclude.
// "missing" includes scopes still awaiting enterprise-admin approval —
// the UI explains that, the probe cannot see Lark's approval queue.
type ProbeStatus string

const (
	ProbeGranted ProbeStatus = "granted"
	ProbeMissing ProbeStatus = "missing"
	ProbeUnknown ProbeStatus = "unknown"
)

// probePermissionCodes are the Lark business codes that mean "the app
// lacks a scope" in the synthetic-target PROBE context. A probe never
// carries a real target, so a scope can only fail at the gateway, up
// front: 99991672 is the canonical no-permission code, 99991002 its
// access-denied sibling. The real-target business-layer codes (230001,
// 230027) are deliberately NOT here: QA live evidence 2026-10-08 showed
// a send-granted install getting 230001 "invalid receive_id" on the
// send probe — with a synthetic target, parameter validation runs
// before any business-layer scope check, so those codes prove the
// gateway passed. See runtimePermissionCodes for where they DO mean
// permission-denied (enricher/hint, real targets).
var probePermissionCodes = map[int]struct{}{
	99991672: {},
	99991002: {},
}

// probeTargetRejectedCodes are the not-exist / invalid-parameter family
// codes QA observed on synthetic-target probes with installs verified
// granted via the data plane (2026-10-08): 99992351 (contact user
// lookup) and 99992354 (message GET, resource download). Like the
// 23xxxx segment below they are target/param rejections — the scope
// check already passed → granted. An explicit allowlist, not a 99992xxx
// range rule, so an unseen family member still conservatively reads as
// unknown instead of silently widening the granted verdict.
var probeTargetRejectedCodes = map[int]struct{}{
	99992351: {},
	99992354: {},
}

// runtimePermissionCodes are the permission-denied business codes on
// REAL targets, where a missing scope can also be enforced at the
// business layer after the gateway: the canonical gateway pair plus
// 230001 (bot not in the chat / invalid receive_id against a real chat)
// and 230027 (missing im:message.group_msg on real group reads).
// Consumed by isRuntimePermissionDenied; identical to the pre-RUYI-546
// rework set, so real-target classification (enricher/hint) is
// unchanged — pinned by the enricher suites.
var runtimePermissionCodes = map[int]struct{}{
	99991672: {},
	99991002: {},
	230001:   {},
	230027:   {},
}

// classifyProbeError turns a probe call's outcome into the tri-state.
//
// Probes always target synthetic ids that cannot exist, so any 23xxxx
// business code — or a known target-rejection code like 99992351/99992354
// — proves the request passed the gateway's scope check and failed later
// on validation: a granted verdict. The gateway-level permission codes
// still mean missing; no code (transport error) or a token error is
// inconclusive: unknown. Unknown never collapses into granted, and an
// admin-pending scope still reads as missing here.
func classifyProbeError(err error) ProbeStatus {
	if err == nil {
		return ProbeGranted
	}
	code, _, ok := larkErrorCodeMsg(err)
	if !ok {
		return ProbeUnknown
	}
	if _, bad := probePermissionCodes[code]; bad {
		return ProbeMissing
	}
	if isTokenError(code) {
		return ProbeUnknown
	}
	if code >= 230000 && code < 240000 {
		// Business-layer validation (deleted 230110/230011/230050,
		// not-exist, invalid params, rate limit 230020…): authz passed.
		return ProbeGranted
	}
	if _, rejected := probeTargetRejectedCodes[code]; rejected {
		// Target/param validation outside the 23xxxx segment (observed
		// on the contact and media/read surfaces): authz passed.
		return ProbeGranted
	}
	return ProbeUnknown
}

// Synthetic probe targets — fixed literals, not random: a probe only
// needs a target the bot cannot have, and determinism keeps tests and
// logs stable. They carry no tenant data and can never resolve.
const (
	probeMessageID = "om_probe_capability_check"
	probeChatID    = ChatID("oc_probe_capability_check")
	probeFileKey   = "file_probe_capability_check"
	probeOpenID    = "ou_probe_capability_check"
)

// ProbeOutcome is the result of one capability probe.
type ProbeOutcome struct {
	Status ProbeStatus
	// Detail is a sanitized one-liner (code + verdict). Probes read no
	// tenant content, so only the business code ever appears here.
	Detail string
}

// ProbeCapability issues the capability's synthetic probe call against
// client and classifies the error. Capabilities without a REST probe
// (the event subscription) return unknown with the reason stated.
func ProbeCapability(ctx context.Context, client APIClient, creds InstallationCredentials, id CapabilityID) ProbeOutcome {
	switch id {
	case CapabilityReadHistory:
		_, err := client.GetMessage(ctx, creds, probeMessageID)
		return probeOutcome(err)
	case CapabilitySendMessages:
		_, err := client.SendTextMessage(ctx, SendTextParams{
			InstallationID: creds,
			ChatID:         probeChatID,
			Text:           "capability probe (cannot deliver: synthetic chat id)",
		})
		return probeOutcome(err)
	case CapabilityMediaResources:
		_, err := client.DownloadMessageResource(ctx, creds, DownloadResourceParams{
			MessageID: probeMessageID,
			FileKey:   probeFileKey,
			Type:      "file",
		})
		return probeOutcome(err)
	case CapabilityContactLookup:
		_, err := client.GetUserName(ctx, creds, probeOpenID)
		return probeOutcome(err)
	default:
		// receive_messages (and any future non-probeable entry): event
		// push has no request to attach an authz failure to.
		return ProbeOutcome{
			Status: ProbeUnknown,
			Detail: "event subscription is not probeable over REST; judged by live message arrival",
		}
	}
}

func probeOutcome(err error) ProbeOutcome {
	status := classifyProbeError(err)
	detail := "probe call succeeded (synthetic target rejected at param level = scope granted)"
	if err != nil {
		if code, _, ok := larkErrorCodeMsg(err); ok {
			detail = fmt.Sprintf("probe call returned business code %d → %s", code, status)
		} else {
			detail = fmt.Sprintf("probe call failed without a Lark business code → %s", status)
		}
	}
	return ProbeOutcome{Status: status, Detail: detail}
}
