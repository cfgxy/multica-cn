package lark

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	// /contact/v3/users/batch for speaker labels in enriched context.
	CapabilityContactLookup CapabilityID = "contact_lookup"
	// CapabilityDriveFileLinks — downloading cloud-drive files shared as
	// bare /file/ links in message text via
	// /drive/v1/files/{file_token}/download (RUYI-572, ADR 005). Deliberately
	// separate from media_resources: different endpoints, different scope
	// family, different failure semantics — merging them would force every
	// attachment-only install to also request drive scopes.
	CapabilityDriveFileLinks CapabilityID = "drive_file_links"
	// CapabilityWikiDocLinks — resolving bare /wiki/ and /docx/ share
	// links: wiki/v2 get_node routes the wiki token to its object and
	// docx/v1 raw_content reads the document text (RUYI-572, ADR 005).
	// The two groups are ANDed because a wiki link needs BOTH: get_node
	// only names the underlying object; its content is unreadable without
	// a docx read scope. (/file/ links stay on drive_file_links — their
	// scope group is disjoint.)
	CapabilityWikiDocLinks CapabilityID = "wiki_doc_links"
)

// CapabilitySpec maps one capability onto the scopes that grant it.
//
// Scopes is AND-of-OR: every inner group must be satisfied by at least
// one of its members. Feishu expresses exactly this shape — e.g. group
// history reads need any of the im:message read scopes AND the
// im:message.group_msg scope. Scope lists verified against the Feishu
// OpenAPI permission tables (open.feishu.cn, 2026-10).
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
var capabilityCatalog = []CapabilitySpec{
	{CapabilityReceiveMessages, [][]string{
		{"im:message.group_at_msg", "im:message.group_at_msg:readonly"},
		{"im:message.p2p_msg", "im:message.p2p_msg:readonly"},
	}, false},
	{CapabilitySendMessages, [][]string{
		{"im:message", "im:message:send_as_bot"},
	}, true},
	{CapabilityReadHistory, [][]string{
		{"im:message", "im:message:readonly", "im:message.history:readonly"},
		{"im:message.group_msg"},
	}, true},
	{CapabilityMediaResources, [][]string{
		{"im:resource"},
	}, true},
	{CapabilityContactLookup, [][]string{
		{"contact:user.base:readonly"},
	}, true},
	// RUYI-572 (ADR 005 §3/§4). drive_file_links: the download endpoint's
	// doc page grants it to any of the four drive read scopes (row fetched
	// from the official page). wiki_doc_links: get_node is granted by the
	// wiki pair (official page) AND raw_content by the docx pair — the
	// docx row is dual-sourced (official permission table mirror + official
	// community article) with QA live-test follow-up, per ADR 005 §9.
	{CapabilityDriveFileLinks, [][]string{
		{"drive:drive", "drive:drive:readonly", "drive:file", "drive:file:readonly"},
	}, true},
	{CapabilityWikiDocLinks, [][]string{
		{"wiki:wiki", "wiki:wiki:readonly"},
		{"docx:document:readonly", "docx:document"},
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
// lacks a scope this endpoint requires". 99991672 is the canonical
// no-permission code; 99991002/230001/230027 are the permission-denied
// codes the recent-context enricher already classifies.
var probePermissionCodes = map[int]struct{}{
	99991672: {},
	99991002: {},
	230001:   {},
	230027:   {},
}

// probeTargetRejectedCodes are family-specific business codes that a
// synthetic-target probe returns AFTER passing the gateway's scope check
// — seeing them proves the scopes are granted. 1061004 is the drive
// family's "file not exist" (ADR 005 §4, official page). This is an
// explicit allowlist, NOT a 106xxxx range rule, on purpose: an unseen
// family member — including a business-layer permission denial, whose
// exact drive-family code the QA live test has not calibrated yet — must
// conservatively read as unknown instead of silently widening the
// granted verdict (ADR 005 §9 calibration item).
var probeTargetRejectedCodes = map[int]struct{}{
	1061004: {},
}

// classifyProbeError turns a probe call's outcome into the tri-state.
//
// Probes always target synthetic ids that cannot exist, so any 23xxxx
// business code other than a known permission code proves the request
// passed the gateway's scope check and failed later on validation — a
// granted verdict. No code (transport error) or a token error is
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
	if _, targetRejected := probeTargetRejectedCodes[code]; targetRejected {
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
	// RUYI-572 share-link probe targets.
	probeDriveFileToken = "drive_probe_capability_check"
	probeWikiToken      = "wiki_probe_capability_check"
	probeDocumentID     = "docx_probe_capability_check"
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
		_, err := client.BatchGetUsers(ctx, creds, []string{probeOpenID})
		return probeOutcome(err)
	case CapabilityDriveFileLinks:
		slc, ok := shareLinkClient(client)
		if !ok {
			return unknownProbeOutcome("drive/Docs client surface unavailable; share-link probes need a ShareLinkClient")
		}
		_, err := slc.DownloadDriveFile(ctx, creds, probeDriveFileToken)
		return probeOutcome(err)
	case CapabilityWikiDocLinks:
		// Two scope groups map to two endpoints; the capability is granted
		// only when BOTH synthetic probes clear their gateway (AND-of-OR,
		// same semantics as the catalog row). Any missing dominates,
		// otherwise any unknown keeps the verdict honest-unknown.
		slc, ok := shareLinkClient(client)
		if !ok {
			return unknownProbeOutcome("drive/Docs client surface unavailable; share-link probes need a ShareLinkClient")
		}
		_, wikiErr := slc.GetWikiNode(ctx, creds, probeWikiToken)
		_, docxErr := slc.GetDocxRawContent(ctx, creds, probeDocumentID)
		return combineProbeOutcomes(
			namedProbeOutcome("get_node", wikiErr),
			namedProbeOutcome("raw_content", docxErr),
		)
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

// shareLinkClient narrows an APIClient to the share-link surface without
// widening APIClient itself (the messageResourceStreamer pattern): the
// stub client and pre-RUYI-572 fakes simply miss the capability.
func shareLinkClient(client APIClient) (ShareLinkClient, bool) {
	slc, ok := client.(ShareLinkClient)
	return slc, ok
}

// unknownProbeOutcome is the honest verdict for a probe that cannot run.
func unknownProbeOutcome(reason string) ProbeOutcome {
	return ProbeOutcome{Status: ProbeUnknown, Detail: reason}
}

// namedProbeOutcome prefixes a probe part's detail with its endpoint so a
// combined verdict names which half failed.
func namedProbeOutcome(endpoint string, err error) ProbeOutcome {
	out := probeOutcome(err)
	out.Detail = endpoint + ": " + out.Detail
	return out
}

// combineProbeOutcomes ANDs the parts: missing dominates (a missing scope
// is actionable), unknown beats granted (an inconclusive half is not
// evidence of grant), granted only when every part is granted.
func combineProbeOutcomes(parts ...ProbeOutcome) ProbeOutcome {
	status := ProbeGranted
	details := make([]string, 0, len(parts))
	for _, p := range parts {
		details = append(details, p.Detail)
		switch p.Status {
		case ProbeMissing:
			status = ProbeMissing
		case ProbeUnknown:
			if status != ProbeMissing {
				status = ProbeUnknown
			}
		}
	}
	return ProbeOutcome{Status: status, Detail: strings.Join(details, "; ")}
}
