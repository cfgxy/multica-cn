package lark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

// larkMsgTypeMergeForward is the msg_type of a "merged & forwarded"
// message — a bundle of other messages a user forwarded as one unit.
// Its own body.content is a fixed sentinel string; the actual forwarded
// messages come back as the extra items[] of a GetMessage call.
const larkMsgTypeMergeForward = "merge_forward"

// defaultMaxForwardChildren caps how many child messages we inline from
// a single forward. Lark itself bounds a merge_forward at 100 messages;
// we mirror that as a safety valve so a pathological bundle can't blow
// up the agent's context. Anything beyond the cap is dropped with a
// visible "... (N more truncated)" marker.
const defaultMaxForwardChildren = 100

// DefaultRecentContextSize is the window the production wiring uses for
// the group-context prefetch: the page_size of the single list call made
// when a user @-mentions the Bot in a group. It is a FETCH budget, not a
// guaranteed rendered count — the trigger message itself and any quoted
// parent are filtered out of the result, so the <recent_context> block
// usually renders one or two fewer lines. 10 keeps the agent's prompt
// meaningfully contextual without bloating it or straining the inbound
// ACK budget (one list call, page_size 10).
const DefaultRecentContextSize = 10

const (
	recentContextEndpoint         = "im/v1/messages.list"
	recentContextMaxFetchAttempts = 2

	recentContextFailureUnknown          = "unknown"
	recentContextFailureChannelUnbound   = "channel_unbound"
	recentContextFailureTimeout          = "timeout"
	recentContextFailurePermissionDenied = "permission_denied"
	recentContextFailureMessageDeleted   = "message_deleted"
	recentContextFailureRateLimited      = "rate_limited"
	recentContextFailureTokenExpired     = "token_expired"
	recentContextFailureTemporary        = "temporary"
)

var errRecentContextChannelUnbound = errors.New("lark enricher: missing chat_id for recent context")

// Enricher expands an inbound message's body with context the user
// EXPLICITLY attached — a quoted reply or a merged-and-forwarded bundle
// — by calling back into Lark's IM API. It runs after the (fast,
// HTTP-free) decoder and before the dispatcher, turning a bare
// "@bot 总结一下" into a body that already carries the referenced
// conversation inline.
//
// It is best-effort by contract: every fetch failure degrades to a
// readable note or placeholder and Enrich NEVER returns an error or
// blocks ingestion. A message with nothing to expand (no parent_id, not
// a merge_forward) is returned untouched without any network call.
type Enricher interface {
	Enrich(ctx context.Context, msg InboundMessage, creds InstallationCredentials) InboundMessage
}

// InboundEnricherConfig tunes the enricher. All fields default.
type InboundEnricherConfig struct {
	// MaxForwardChildren caps inlined forward children. <=0 uses
	// defaultMaxForwardChildren.
	MaxForwardChildren int
	// RecentContextSize caps how many surrounding group messages the
	// enricher prefetches and inlines as a <recent_context> block when a
	// user @-mentions the Bot in a group. <=0 DISABLES the prefetch
	// entirely (only explicitly-attached quote/forward context is used);
	// the production wiring sets DefaultRecentContextSize. Values above
	// Lark's 50-per-page cap are clamped by the client.
	RecentContextSize int
	// Logger receives best-effort warnings about fetch failures. Nil
	// uses slog.Default().
	Logger *slog.Logger
	// Hints, when set, receives this enricher's runtime permission
	// observations: a permission-class fetch failure posts the matching
	// capability's in-chat authorization hint card (deduped, async,
	// silent-degrade); a successful call re-arms it. Nil disables the
	// feature entirely.
	Hints *PermissionHintSender
}

type inboundEnricher struct {
	client             APIClient
	maxForwardChildren int
	recentContextSize  int
	logger             *slog.Logger
	hints              *PermissionHintSender
	names              *userNameCache
}

// userNameCache memoizes open_id -> display name per app, mirroring
// CC Connect's user-name sync.Map (entries are never invalidated —
// display names change rarely and a stale name is cosmetic). The
// adaptation is the bound: FIFO eviction at userNameCacheMaxEntries so
// a pathological tenant cannot grow the map forever. Keyed by
// app_id+open_id because open_ids are app-scoped.
type userNameCache struct {
	mu    sync.Mutex
	max   int
	byKey map[string]string
	order []string
}

// userNameCacheMaxEntries bounds the memoized name entries. A busy
// 100-person chat resolves ~100 entries per installation; 1024 covers
// every realistic workspace many times over.
const userNameCacheMaxEntries = 1024

func newUserNameCache() *userNameCache {
	return &userNameCache{
		max:   userNameCacheMaxEntries,
		byKey: make(map[string]string),
	}
}

func (c *userNameCache) get(appID, openID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name, ok := c.byKey[appID+"\x00"+openID]
	return name, ok
}

func (c *userNameCache) put(appID, openID, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := appID + "\x00" + openID
	_, exists := c.byKey[key]
	if !exists && len(c.order) >= c.max {
		delete(c.byKey, c.order[0])
		c.order = c.order[1:]
	}
	if !exists {
		c.order = append(c.order, key)
	}
	c.byKey[key] = name
}

// NewInboundEnricher builds an Enricher backed by the given Lark API
// client. The client supplies GetMessage; everything else (flattening,
// block assembly, speaker labelling) is local.
func NewInboundEnricher(client APIClient, cfg InboundEnricherConfig) Enricher {
	if cfg.MaxForwardChildren <= 0 {
		cfg.MaxForwardChildren = defaultMaxForwardChildren
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &inboundEnricher{
		client:             client,
		maxForwardChildren: cfg.MaxForwardChildren,
		recentContextSize:  cfg.RecentContextSize,
		logger:             cfg.Logger,
		hints:              cfg.Hints,
		names:              newUserNameCache(),
	}
}

// Enrich rewrites msg.Body to inline surrounding group context and/or
// any quoted-reply parent and/or forwarded bundle. Composition order
// goes broadest-to-narrowest: the surrounding group history first, then
// the explicitly-quoted parent (a specific reference), then the message's
// own content (or, for a forward, the rendered transcript).
//
//	<recent_context …>…</recent_context>
//
//	<quoted_message …>…</quoted_message>
//
//	<[sender name]: the user's own message, or the forwarded transcript>
//
// The <recent_context> block is only produced for a group message
// addressed to the Bot, and only when RecentContextSize > 0 — it answers
// MUL-3084 (the Bot saw only the single @-ed line, never the surrounding
// conversation). It is the one fetch here NOT triggered by something the
// user explicitly attached. When the @-mention arrives inside a Lark topic
// (话题) the window is scoped to that topic, so a topic's context never
// includes a sibling topic's messages (#5835 — see fetchRecentItems).
//
// In group chats, every speaker across ALL blocks (recent + quoted +
// forwarded) and the sender who @-mentioned the Bot are resolved to real
// display names via ONE Contact batch call, so the agent reads
// "[Alice]: …" rather than "[User 1]: …" and knows who addressed it. This
// is why the quote/forward items are fetched up front (Phase 1) before
// names are resolved (Phase 2). Unresolved senders fall back to positional
// "User N"; resolution is best-effort and never blocks. p2p chats keep
// positional labels (identity is unambiguous in a 1:1).
//
// Persistence note: like the quoted/forwarded blocks, the rewritten Body
// is persisted into the addressed turn's chat_message.content downstream
// (AppendUserMessage). Inlining nearby group messages — including ones
// from senders who did not address the Bot — into a member's addressed
// turn is an accepted product decision for MUL-3084. It does NOT relax
// the MUL-2671 drop-audit invariant: a non-addressed group message still
// never creates its own session row, and is only ever surfaced as read-
// context attached to a turn a workspace member explicitly directed at
// the Bot.
func (e *inboundEnricher) Enrich(ctx context.Context, msg InboundMessage, creds InstallationCredentials) InboundMessage {
	freshSource := msg.CommandBody
	if freshSource == "" {
		freshSource = msg.Body
	}
	startChat := false
	if control, ok := engine.ParseControlCommand(freshSource); ok {
		msg.Body = control.Body
		switch control.Kind {
		case engine.ControlCommandFreshSession:
			msg.ForceFreshSession = true
		case engine.ControlCommandNewChat:
			// A new Chat must not inherit adapter-generated recent context from
			// the route's previous Chat. Explicit quote/forward context is still
			// expanded below because the user attached it to this command.
			startChat = true
		}
	}

	isForward := msg.MessageType == larkMsgTypeMergeForward
	wantRecent := !startChat && e.recentContextSize > 0 && msg.ChatType == ChatTypeGroup && msg.AddressedToBot
	if msg.ParentID == "" && !isForward && !wantRecent {
		// Nothing to expand and no group prefetch wanted — no network call.
		return msg
	}
	// If the transport isn't wired (stub client on a deployment without
	// a Lark app), skip rather than stamp every reply with a fetch
	// error. Body stays whatever the decoder produced.
	if e.client == nil || !e.client.IsConfigured() {
		return msg
	}

	// Phase 1 — fetch every set of messages we may render. Each is
	// best-effort; its error is handled where the block is rendered. The
	// fetches are independent reads, so they run CONCURRENTLY (RUYI-448:
	// serial list → get → contact lookup made the name resolution the
	// third sequential round-trip inside the ~2s EnrichTimeout — exactly
	// the call that starved first, degrading every speaker to "User N").
	// Running list ∥ get keeps the Contact lookups off the serial path.
	// Hint observations and the media-descriptor harvest stay on this
	// goroutine, after the wait, so ordering there is deterministic. We
	// fetch up front (rather than fetch-and-render per block) so Phase 2
	// can resolve display names for EVERY speaker across ALL blocks in
	// one pass — otherwise a quoted/forwarded sender that isn't in the
	// recent window would fall back to "User N".
	var recentItems []LarkMessage
	var recentErr error
	var quotedItems []LarkMessage
	var quotedErr error
	var forwardItems []LarkMessage
	var forwardErr error

	var fetches sync.WaitGroup
	if wantRecent {
		fetches.Add(1)
		go func() {
			defer fetches.Done()
			recentItems, recentErr = e.fetchRecentItems(ctx, creds, msg)
		}()
	}
	if msg.ParentID != "" {
		fetches.Add(1)
		go func() {
			defer fetches.Done()
			quotedItems, quotedErr = e.client.GetMessage(ctx, creds, msg.ParentID)
		}()
	}
	if isForward {
		fetches.Add(1)
		go func() {
			defer fetches.Done()
			forwardItems, forwardErr = e.client.GetMessage(ctx, creds, msg.MessageID)
		}()
	}
	fetches.Wait()

	if wantRecent {
		if recentErr != nil {
			e.hints.ObserveDenied(ctx, creds, msg.ChatID, CapabilityReadHistory, recentErr)
		} else {
			e.hints.ObserveSuccess(creds.AppID, msg.ChatID, CapabilityReadHistory)
		}
	}
	if recentErr == nil && wantRecent && msg.SenderOpenID != "" {
		triggerTime := parseLarkMillis(msg.CreateTime)
		for _, item := range recentItems {
			itemTime := parseLarkMillis(item.CreateTime)
			if triggerTime == 0 || itemTime == 0 || itemTime >= triggerTime ||
				item.MessageID == "" || item.SenderType != "user" || item.SenderID != string(msg.SenderOpenID) ||
				item.ThreadID != msg.ThreadID {
				continue
			}
			media := EnrichedMediaMessage{MessageID: item.MessageID, MessageType: item.MessageType, Content: item.Content}
			if messageCarriesMediaOrLinks(InboundMessage{MessageID: media.MessageID, MessageType: media.MessageType, Content: media.Content}) {
				msg.RecentMedia = append(msg.RecentMedia, media)
			}
		}
	}
	if msg.ParentID != "" {
		if quotedErr != nil {
			e.hints.ObserveDenied(ctx, creds, msg.ChatID, CapabilityReadHistory, quotedErr)
		} else {
			e.hints.ObserveSuccess(creds.AppID, msg.ChatID, CapabilityReadHistory)
		}
		if quotedErr == nil && len(quotedItems) > 0 {
			// The quoted parent may itself carry downloadable media (a
			// reply to a file/image message). Capture its descriptors so the
			// downstream media resolver ingests the attachment through the
			// same path as the trigger's own media. Only the direct parent
			// is harvested — a merge_forward parent renders its children as
			// a text transcript without attaching their files, keeping the
			// download fan-out bounded on this ACK-latency-sensitive path.
			parent := quotedItems[0]
			if parent.MessageID != "" && !parent.Deleted && parent.MessageType != larkMsgTypeMergeForward &&
				messageCarriesMediaOrLinks(InboundMessage{MessageID: parent.MessageID, MessageType: parent.MessageType, Content: parent.Content}) {
				msg.QuotedMedia = append(msg.QuotedMedia, EnrichedMediaMessage{
					MessageID:   parent.MessageID,
					MessageType: parent.MessageType,
					Content:     parent.Content,
				})
			}
		}
	}
	if isForward {
		if forwardErr != nil {
			e.hints.ObserveDenied(ctx, creds, msg.ChatID, CapabilityReadHistory, forwardErr)
		} else {
			e.hints.ObserveSuccess(creds.AppID, msg.ChatID, CapabilityReadHistory)
		}
	}

	// Phase 2 — resolve display names for every speaker we're about to
	// render (recent + quoted + forwarded) plus the sender who @-mentioned
	// the Bot, in one batch. Group chats only; p2p keeps positional labels
	// (identity is unambiguous in a 1:1). Unresolved ids fall back to
	// "User N" per speakerLabeler.
	var names map[string]string
	if msg.ChatType == ChatTypeGroup {
		ids := senderOpenIDs(recentItems)
		ids = append(ids, senderOpenIDs(quotedItems)...)
		ids = append(ids, senderOpenIDs(forwardItems)...)
		if msg.SenderOpenID != "" {
			ids = append(ids, string(msg.SenderOpenID))
		}
		names = e.resolveNames(ctx, creds, msg.ChatID, ids)
	}

	// Phase 3 — render broadest-to-narrowest with the complete name map.
	var b strings.Builder
	if wantRecent {
		if recentErr != nil {
			b.WriteString(recentContextUnavailableLine(classifyRecentContextFetchError(recentErr).category))
		} else if len(recentItems) > 0 {
			b.WriteString(e.renderRecentContextBlock(recentItems, names))
		}
	}
	if msg.ParentID != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(e.renderQuotedBlock(msg.ParentID, quotedItems, quotedErr, names))
	}

	var core string
	if isForward {
		if forwardErr != nil {
			e.logger.Warn("lark enricher: forward fetch failed", "message_id", msg.MessageID, "err", forwardErr)
			core = forwardedErrorBlock()
		} else {
			core = e.renderForwardedItems(forwardItems, msg.MessageID, names)
		}
	} else {
		core = msg.Body
		// Label the user's own message with their real name so the agent
		// knows WHO @-mentioned it — not just what they said. Only when the
		// name resolved (group path); otherwise the body passes through.
		if name := names[string(msg.SenderOpenID)]; name != "" {
			core = fmt.Sprintf("[%s]: %s", name, msg.Body)
		}
	}
	if b.Len() > 0 && core != "" {
		b.WriteString("\n\n")
	}
	b.WriteString(core)

	msg.Body = b.String()
	return msg
}

// senderOpenIDs returns the distinct non-app sender open_ids across the
// given messages, in first-appearance order — the input set for a
// Contact name lookup.
func senderOpenIDs(msgs []LarkMessage) []string {
	seen := make(map[string]bool, len(msgs))
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.SenderType == "app" || m.SenderID == "" || seen[m.SenderID] {
			continue
		}
		seen[m.SenderID] = true
		out = append(out, m.SenderID)
	}
	return out
}

// resolveNamesMaxInFlight bounds the concurrent single-user lookups.
// uniq speakers per enrich are bounded by the recent-context page size
// (≤50) plus quote/forward senders; the semaphore keeps a cold-cache
// burst well under Lark's 50 QPS per-app budget shared with the other
// enrich fetches.
const resolveNamesMaxInFlight = 8

// resolveNames resolves open_ids to display names over the single-user
// Contact lookup (CC Connect parity), best-effort: per-user failures
// (restricted contact scope, invisible user, transport error) log and
// leave that speaker to the positional "User N" fallback rather than
// blocking ingestion. Duplicate / empty ids are dropped first; a small
// per-app cache absorbs repeat lookups across messages. A
// permission-class failure feeds the contact_lookup hint; a pass with
// no permission-class failure re-arms it.
func (e *inboundEnricher) resolveNames(ctx context.Context, creds InstallationCredentials, chatID ChatID, ids []string) map[string]string {
	uniq := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return nil
	}

	out := make(map[string]string, len(uniq))
	var misses []string
	for _, id := range uniq {
		if name, ok := e.names.get(creds.AppID, id); ok {
			out[id] = name
			continue
		}
		misses = append(misses, id)
	}
	if len(misses) == 0 {
		return out
	}

	var (
		mu       sync.Mutex
		denied   error
		resolved int
	)
	var wg sync.WaitGroup
	sem := make(chan struct{}, resolveNamesMaxInFlight)
	for _, id := range misses {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			name, err := e.client.GetUserName(ctx, creds, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil && isRuntimePermissionDenied(err):
				// Scope gap for the whole installation: surface once via
				// the hint deduper; keep degrading per-speaker meanwhile.
				if denied == nil {
					denied = err
				}
				e.logger.Debug("lark enricher: speaker name lookup denied", "open_id", id, "err", err)
			case err != nil || name == "":
				// CC Connect's degradation branch: log quietly, skip the
				// speaker (positional label), cache nothing.
				e.logger.Debug("lark enricher: speaker name lookup failed", "open_id", id, "err", err)
			default:
				e.names.put(creds.AppID, id, name)
				out[id] = name
				resolved++
			}
		}(id)
	}
	wg.Wait()

	if denied != nil {
		e.logger.Warn("lark enricher: speaker name resolution denied", "ids", len(misses), "err", denied)
		e.hints.ObserveDenied(ctx, creds, chatID, CapabilityContactLookup, denied)
	} else if resolved > 0 || len(misses) == 0 {
		e.hints.ObserveSuccess(creds.AppID, chatID, CapabilityContactLookup)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fetchRecentItems pulls the recent group window and returns the
// messages to render — the trigger message itself and the directly-quoted
// parent (which gets its own <quoted_message> block) filtered out, sorted
// oldest-first. A fetch failure is returned to the caller (which renders a
// safe, readable degradation note); it never blocks ingestion.
//
// When the trigger arrived inside a Lark topic (msg.ThreadID != ""), the
// window is scoped to that topic (container_id_type=thread) so sibling
// topics that share the chat_id can't leak into this topic's context or
// its persisted turn (#5835). Because the thread container rejects
// end_time, the topic path anchors to the trigger time CLIENT-side; it
// also fail-closes on thread_id — any returned item whose thread_id is
// missing or does not match is dropped rather than trusted. A topic fetch
// failure degrades exactly like the chat path and NEVER falls back to a
// chat-wide fetch (that would re-open the leak). Outside a topic the chat
// path is unchanged: anchored to the trigger time via end_time.
func (e *inboundEnricher) fetchRecentItems(ctx context.Context, creds InstallationCredentials, msg InboundMessage) ([]LarkMessage, error) {
	if msg.ChatID == "" {
		classified := classifyRecentContextFetchError(errRecentContextChannelUnbound)
		e.logRecentContextFetchFailure(msg, errRecentContextChannelUnbound, classified, 0)
		return nil, errRecentContextChannelUnbound
	}

	// Lark sends create_time as epoch millis; a missing/unparseable time
	// yields 0. The chat path converts it to seconds for end_time; the
	// thread path uses the raw millis for the client-side anchor below.
	triggerMillis := parseLarkMillis(msg.CreateTime)
	params := ListMessagesParams{
		ChatID:   msg.ChatID,
		PageSize: e.recentContextSize,
	}
	if msg.ThreadID != "" {
		// Topic-scoped fetch: no end_time (the thread container rejects it);
		// the window is anchored client-side below.
		params.ThreadID = msg.ThreadID
	} else {
		// 0 tells the client "no end_time" (newest N).
		params.EndTime = triggerMillis / 1000
	}
	var items []LarkMessage
	var err error
	for attempt := 1; attempt <= recentContextMaxFetchAttempts; attempt++ {
		items, err = e.client.ListChatMessages(ctx, creds, params)
		if err == nil {
			if attempt > 1 {
				e.logger.Info("lark enricher: recent context fetch recovered after retry",
					"layer", "lark_inbound_enricher",
					"endpoint", recentContextEndpoint,
					"status", "recovered",
					"attempts", attempt,
					"chat_id", string(msg.ChatID),
					"message_id", msg.MessageID)
			}
			break
		}
		classified := classifyRecentContextFetchError(err)
		// A retry only helps while the shared enrichment budget still has
		// time left. Both attempts reuse one ctx (ws_connector caps the
		// whole Enrich at EnrichTimeout, ~2s), so once ctx is done a second
		// call fails immediately — degrade now instead of burning a doomed
		// request. This is why a first-attempt deadline never "recovers".
		if !classified.retryable || attempt == recentContextMaxFetchAttempts || ctx.Err() != nil {
			e.logRecentContextFetchFailure(msg, err, classified, attempt)
			return nil, err
		}
		e.logger.Warn("lark enricher: recent context fetch failed; retrying",
			"layer", "lark_inbound_enricher",
			"endpoint", recentContextEndpoint,
			"status", "retrying",
			"category", classified.category,
			"retryable", classified.retryable,
			"attempt", attempt,
			"next_attempt", attempt+1,
			"chat_id", string(msg.ChatID),
			"message_id", msg.MessageID,
			"err", err)
	}

	exclude := map[string]bool{msg.MessageID: true}
	if msg.ParentID != "" {
		exclude[msg.ParentID] = true
	}
	inThread := msg.ThreadID != ""
	kept := make([]LarkMessage, 0, len(items))
	for _, it := range items {
		if exclude[it.MessageID] {
			continue
		}
		// The Bot's markdown replies are sent as schema-2.0 interactive
		// cards, which flatten to a zero-signal "[interactive card]"
		// placeholder — drop them rather than render noise (#5835).
		if it.SenderType == "app" && it.MessageType == "interactive" {
			continue
		}
		if inThread {
			// Fail-closed topic isolation: the thread container should only
			// return this topic's messages, but if Lark ever returns an item
			// with a missing or mismatched thread_id, drop it rather than
			// risk leaking a sibling topic's content into this topic.
			if it.ThreadID != msg.ThreadID {
				continue
			}
			// The thread container ignores end_time, so anchor client-side:
			// drop anything created strictly after the @-mention moment. A
			// zero trigger time (unparseable) disables the anchor.
			if triggerMillis > 0 && parseLarkMillis(it.CreateTime) > triggerMillis {
				continue
			}
		}
		kept = append(kept, it)
	}

	// The list endpoint returns newest-first; render oldest-first so the
	// transcript reads top-to-bottom like the chat does.
	sort.SliceStable(kept, func(i, j int) bool {
		return parseLarkMillis(kept[i].CreateTime) < parseLarkMillis(kept[j].CreateTime)
	})
	return kept, nil
}

func (e *inboundEnricher) logRecentContextFetchFailure(msg InboundMessage, err error, classified recentContextFetchClassification, attempts int) {
	e.logger.Warn("lark enricher: recent context fetch failed",
		"layer", "lark_inbound_enricher",
		"endpoint", recentContextEndpoint,
		"status", "failed",
		"category", classified.category,
		"retryable", classified.retryable,
		"attempts", attempts,
		"chat_id", string(msg.ChatID),
		"message_id", msg.MessageID,
		"err", err)
}

// renderRecentContextBlock renders the surrounding conversation as a
// <recent_context> block: one "[<speaker>]: <text>" line per message,
// oldest-first, speakers labeled with real names from `names` (falling
// back to positional "User N"). Callers pass a non-empty `kept`.
func (e *inboundEnricher) renderRecentContextBlock(kept []LarkMessage, names map[string]string) string {
	labeler := newSpeakerLabeler(names)
	lines := make([]string, 0, len(kept))
	for _, m := range kept {
		label := labeler.label(m)
		var text string
		switch {
		case m.MessageType == larkMsgTypeMergeForward:
			text = "[merge_forward, expand manually]"
		default:
			text = e.flattenMessage(m)
			if text == "" {
				text = "[empty message]"
			}
		}
		lines = append(lines, fmt.Sprintf("[%s]: %s", label, text))
	}
	return fmt.Sprintf("<recent_context count=\"%d\">\n%s\n</recent_context>",
		len(kept), strings.Join(lines, "\n"))
}

type recentContextFetchClassification struct {
	category  string
	retryable bool
}

func classifyRecentContextFetchError(err error) recentContextFetchClassification {
	if err == nil {
		return recentContextFetchClassification{category: recentContextFailureUnknown}
	}
	if errors.Is(err, errRecentContextChannelUnbound) {
		return recentContextFetchClassification{category: recentContextFailureChannelUnbound}
	}
	// A Lark business code names the failure exactly, so prefer it over
	// the text heuristics below — read through larkErrorCodeMsg it is
	// found in either shape that carries one: the 2xx envelope a call
	// site rejected (*APIError) and the non-2xx reply the HTTP client
	// returns. Only a code that resolves to a real category short-
	// circuits; anything else falls through, so a status-only signal
	// like "http 403" still classifies on the error text.
	if code, msg, ok := larkErrorCodeMsg(err); ok {
		if cls := classifyRecentContextAPIError(code, msg); cls.category != recentContextFailureUnknown {
			return cls
		}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return recentContextFetchClassification{category: recentContextFailureTimeout, retryable: true}
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "missing chat_id") || strings.Contains(msg, "missing chat id"):
		return recentContextFetchClassification{category: recentContextFailureChannelUnbound}
	case containsAny(msg, "code=99991002", "code=230001", "code=230027", "no permission", "permission denied", "insufficient permissions", "forbidden", "http 403"):
		return recentContextFetchClassification{category: recentContextFailurePermissionDenied}
	case containsAny(msg, "code=230110", "code=230011", "code=230050", "deleted", "recalled", "not visible", "invisible"):
		return recentContextFetchClassification{category: recentContextFailureMessageDeleted}
	case containsAny(msg, "code=99991663", "code=99991664"):
		return recentContextFetchClassification{category: recentContextFailureTokenExpired, retryable: true}
	case containsAny(msg, "code=230020", "rate limit", "rate_limit", "http 429"):
		// Not retryable: the client drops Retry-After, and an immediate
		// second call within the same budget almost always re-hits the
		// limit while doubling list load on an already-throttled tenant.
		return recentContextFetchClassification{category: recentContextFailureRateLimited}
	case containsAny(msg, "deadline exceeded", "timeout", "timed out"):
		return recentContextFetchClassification{category: recentContextFailureTimeout, retryable: true}
	case containsAny(msg, "http 500", "http 502", "http 503", "http 504", "connection reset", "connection refused", "temporary"):
		return recentContextFetchClassification{category: recentContextFailureTemporary, retryable: true}
	default:
		return recentContextFetchClassification{category: recentContextFailureUnknown}
	}
}

func classifyRecentContextAPIError(code int, msg string) recentContextFetchClassification {
	switch {
	case code == 99991002 || code == 230001 || code == 230027:
		return recentContextFetchClassification{category: recentContextFailurePermissionDenied}
	case code == 230110 || code == 230011 || code == 230050:
		return recentContextFetchClassification{category: recentContextFailureMessageDeleted}
	case isTokenError(code):
		return recentContextFetchClassification{category: recentContextFailureTokenExpired, retryable: true}
	case code == 230020:
		// See classifyRecentContextFetchError: rate limits degrade rather
		// than retry, since the client drops Retry-After.
		return recentContextFetchClassification{category: recentContextFailureRateLimited}
	}
	return classifyRecentContextFetchError(errors.New(msg))
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func recentContextUnavailableLine(category string) string {
	switch category {
	case recentContextFailureChannelUnbound:
		return "[Recent Lark context unavailable: chat binding is missing. Continuing with the latest message.]"
	case recentContextFailurePermissionDenied:
		return "[Recent Lark context unavailable: the bot cannot read this chat history. Continuing with the latest message.]"
	case recentContextFailureMessageDeleted:
		return "[Recent Lark context unavailable: the referenced chat history is deleted or no longer visible. Continuing with the latest message.]"
	case recentContextFailureTimeout, recentContextFailureRateLimited, recentContextFailureTokenExpired, recentContextFailureTemporary:
		return "[Recent Lark context temporarily unavailable; continuing with the latest message.]"
	default:
		return "[Recent Lark context unavailable; continuing with the latest message.]"
	}
}

// renderQuotedBlock renders a <quoted_message> block from the already-
// fetched GetMessage(parentID) result. A parent that is itself a
// merge_forward nests a <forwarded_messages> transcript inside the quoted
// block (the GetMessage response already carries both the forward
// sentinel and its children). A fetch error / empty / deleted parent
// degrades to the documented error block. Speakers are labeled from
// `names` (the shared, already-resolved map), falling back to "User N".
func (e *inboundEnricher) renderQuotedBlock(parentID string, items []LarkMessage, err error, names map[string]string) string {
	if err != nil || len(items) == 0 {
		e.logger.Warn("lark enricher: quoted parent fetch failed",
			"parent_id", parentID, "items", len(items), "err", err)
		return quotedErrorBlock(parentID)
	}
	parent := items[0]
	if parent.Deleted {
		return quotedErrorBlock(parentID)
	}

	labeler := newSpeakerLabeler(names)
	sender := labeler.label(parent)

	if parent.MessageType == larkMsgTypeMergeForward {
		inner := e.renderForwardedItems(items, parentID, names)
		return wrapQuoted(parentID, sender, larkMsgTypeMergeForward, inner)
	}
	text := e.flattenMessage(parent)
	if text == "" {
		text = "[empty message]"
	}
	if note := feishuShareLinkNote(text); note != "" {
		text += "\n" + note
	}
	return wrapQuoted(parentID, sender, parent.MessageType, text)
}

// feishuShareLinkNote returns the degradation note for a quoted TEXT parent
// whose entire content is one bare Feishu share link — a cloud file, a docx
// or a wiki page (RUYI-448: the HCM xlsx incident — Lark renders such a
// message as a file card in the UI, but over the API it is msg_type=text
// with a URL, so there is no file_key for the quoted-media pipeline to
// capture. RUYI-572 builds the resolver side for /file/, /docx/ and /wiki/
// links; the note stays as the fixed degradation copy for installs without
// the capability (or document-level denials) and now covers all three
// families, telling the agent to ask for an attachment when the linked
// content did not arrive attached. The URL itself is kept verbatim. Empty
// for anything that is not a bare Feishu share link, so ordinary text,
// other Feishu families and non-Feishu URLs never see it.
func feishuShareLinkNote(text string) string {
	if !isBareFeishuShareLink(text) {
		return ""
	}
	return "[Note: the quoted message is a Feishu file/doc/wiki share link, not " +
		"an attachment. If the linked content was not attached automatically, the " +
		"bot cannot download it with its current permissions — ask the sender to " +
		"send the file directly as an attachment instead of fetching the link.]"
}

// renderForwardedItems renders the children of a forward whose own
// record id is forwardID. Children are time-ordered, capped, and each
// rendered as "[<speaker>]: <text>"; a child that is itself a forward is
// not recursed into (it gets a manual-expand placeholder) so the HTTP
// fan-out on the ACK-latency-sensitive inbound path stays bounded.
func (e *inboundEnricher) renderForwardedItems(items []LarkMessage, forwardID string, names map[string]string) string {
	// The verified contract is that GetMessage(forward_id) returns one
	// level of bundling: [sentinel, direct-children…]. We therefore
	// treat every non-sentinel item as a direct child. We filter by id
	// (not by upper_message_id == forwardID) on purpose: a strict
	// upper_message_id match would silently DROP a real child if Lark
	// ever returned one with that field unpopulated. A child that is
	// itself a forward is rendered as a manual-expand placeholder below
	// rather than recursed into, so grandchildren are never inlined.
	children := make([]LarkMessage, 0, len(items))
	for _, it := range items {
		if it.MessageID == forwardID {
			continue // the forward sentinel itself
		}
		children = append(children, it)
	}
	total := len(children)
	if total == 0 {
		return "<forwarded_messages count=\"0\">\n[no forwarded content available]\n</forwarded_messages>"
	}

	sort.SliceStable(children, func(i, j int) bool {
		return parseLarkMillis(children[i].CreateTime) < parseLarkMillis(children[j].CreateTime)
	})

	truncated := 0
	if total > e.maxForwardChildren {
		truncated = total - e.maxForwardChildren
		children = children[:e.maxForwardChildren]
	}

	labeler := newSpeakerLabeler(names)
	lines := make([]string, 0, len(children))
	for _, c := range children {
		label := labeler.label(c)
		var text string
		switch {
		case c.MessageType == larkMsgTypeMergeForward:
			text = "[nested merge_forward, expand manually]"
		default:
			text = e.flattenMessage(c)
			if text == "" {
				text = "[empty message]"
			}
		}
		lines = append(lines, fmt.Sprintf("[%s]: %s", label, text))
	}
	body := strings.Join(lines, "\n")
	if truncated > 0 {
		body += fmt.Sprintf("\n... (%d more truncated)", truncated)
	}
	return fmt.Sprintf("<forwarded_messages count=\"%d\">\n%s\n</forwarded_messages>", total, body)
}

// flattenMessage turns one fetched message into plain text: structural
// flatten by msg_type, then @_user_N placeholder resolution against the
// message's own mentions. The bot mention is NOT stripped here (unlike
// the inbound decoder) — a quoted / forwarded message is historical
// context, not a fresh trigger, so passing empty bot identifiers leaves
// every @-mention rendered as a readable @name.
func (e *inboundEnricher) flattenMessage(m LarkMessage) string {
	if m.Deleted {
		return "[deleted message]"
	}
	raw := flattenContent(m.MessageType, m.Content)
	if raw == "" {
		return ""
	}
	if named := mediaPlaceholderWithFilename(m.MessageType, m.Content); named != "" {
		raw = named
	}
	return resolveMentions(raw, restMentionsToEvent(m.Mentions), "", "")
}

// restMentionsToEvent adapts the IM REST mention shape (flat string id)
// to the WS-event larkMention shape resolveMentions consumes, so a
// single mention-resolution implementation serves both ingress paths.
func restMentionsToEvent(ms []LarkMessageMention) []larkMention {
	if len(ms) == 0 {
		return nil
	}
	out := make([]larkMention, 0, len(ms))
	for _, m := range ms {
		lm := larkMention{Key: m.Key, Name: m.Name}
		lm.ID.OpenID = m.ID
		out = append(out, lm)
	}
	return out
}

func wrapQuoted(messageID, sender, msgType, inner string) string {
	return fmt.Sprintf("<quoted_message message_id=%q sender=%q type=%q>\n%s\n</quoted_message>",
		messageID, sender, msgType, inner)
}

func quotedErrorBlock(messageID string) string {
	return fmt.Sprintf("<quoted_message message_id=%q type=\"error\">[unable to fetch]</quoted_message>", messageID)
}

func forwardedErrorBlock() string {
	return "<forwarded_messages type=\"error\">[unable to fetch]</forwarded_messages>"
}

func parseLarkMillis(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// speakerLabeler assigns stable, human-readable labels to the senders
// within one rendered block. Lark message items carry only a sender id
// (no display name in the payload), so the enricher resolves real names
// out of band via the Contact API and passes them in as a sender-id ->
// name map. A sender present in that map is labeled with their real
// name; one that is not (restricted contact scope, deactivated user,
// name lookup failed) falls back to "User 1", "User 2", … in
// first-appearance order. App senders are always "Bot".
type speakerLabeler struct {
	names map[string]string // resolved open_id -> display name (may be nil)
	seen  map[string]string
	n     int
}

func newSpeakerLabeler(names map[string]string) *speakerLabeler {
	return &speakerLabeler{names: names, seen: make(map[string]string)}
}

func (l *speakerLabeler) label(m LarkMessage) string {
	if m.SenderType == "app" {
		return "Bot"
	}
	key := m.SenderID
	if key == "" {
		key = "unknown"
	}
	if lbl, ok := l.seen[key]; ok {
		return lbl
	}
	var lbl string
	if name := l.names[key]; name != "" {
		lbl = name
	} else {
		l.n++
		lbl = fmt.Sprintf("User %d", l.n)
	}
	l.seen[key] = lbl
	return lbl
}
