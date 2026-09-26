package lark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Outbound file delivery for Feishu, the second half of the two-layer file
// policy: the server declares chat_channel_delivers_files on the claim only
// when a deployment actually wired object storage, and this is the path that
// declaration promises. The two are decided by ONE condition at the router
// (see cmd/server/router.go) — an agent told files work on a deployment whose
// storage is absent would produce a file every turn and lose every one.
//
// The shape follows internal/integrations/wecom/outbound_media.go deliberately.
// The failure modes are the platform-independent ones (storage unreadable,
// upload refused, verdict never arrived) and the questions an operator asks are
// the same, so a second vocabulary for them would only mean two dashboards
// nobody can compare.

// mediaObjectStore is the slice of storage.Storage this path needs: the
// attachment row carries the object's URL, and these two turn it back into
// bytes.
type mediaObjectStore interface {
	KeyFromURL(rawURL string) string
	GetReader(ctx context.Context, key string) (io.ReadCloser, error)
}

// What the user is told, and there are three of these because there are three
// things that can be true — telling them apart is the point (see deliveryState).
// Hardcoded Chinese like the rest of this adapter's user-facing strings.
const (
	// mediaSendFailedText — we know it did not arrive. Definite, because
	// claiming a definite failure that later turns out to be a delivery is how
	// a user ends up ignoring the notice.
	mediaSendFailedText = "⚠️ 有文件没能发出来，我这边保留着，需要的话我再试一次。"

	// mediaSendUnknownText — the request went out and no verdict came back, so
	// the file may be in the chat already. The wording has to survive both
	// endings: it must not say "failed" to someone looking at the file, and it
	// must not say "sent" to someone who never got it. It also explains why
	// nothing is resent automatically, since that is the obvious next question
	// and the answer is that a duplicate cannot be taken back.
	mediaSendUnknownText = "⚠️ 有文件我没拿到飞书的发送结果，可能已经发到了、也可能没有。我不会自动重发，免得发重了；你那边没看到的话说一声，我再发一次。"

	// mediaLookupFailedText — the failure is on our side and before the
	// question was even answered: we could not read what was attached to this
	// reply, so we do not know whether there was a file. Saying nothing here is
	// what leaves a user waiting for something that was never attempted.
	mediaLookupFailedText = "⚠️ 我这边没查到这条回答带没带文件，所以要是有，这次没发出来。需要的话我再试一次。"
)

// attachmentBudget bounds one answer's whole attachment delivery — reading
// every object, uploading it, and sending it. Generous because a 30 MiB upload
// is not fast, and nothing is waiting on it.
const attachmentBudget = 5 * time.Minute

// The ceilings Feishu applies to outbound media: 10 MiB for an image sent as an
// image, 30 MiB for a file. Bytes past the image ceiling still travel — as a
// file — because a file card the user can open beats an image the server
// refused.
const (
	maxOutboundImageBytes = 10 << 20
	maxOutboundFileBytes  = 30 << 20
)

// feishuFileTypeStream is Feishu's catch-all file_type. Anything outside the
// enum (opus, mp4, pdf, doc, xls, ppt) is uploaded as stream, which is what a
// zip or a csv has to be.
const feishuFileTypeStream = "stream"

// The closed sets behind the metric labels. Bounded by construction — no
// installation, workspace or session id ever reaches a label, the same rule
// internal/metrics forbiddenMetricLabels enforces.
const (
	// dropReasonTooLarge — the recorded size is past Feishu's file ceiling, so
	// it was refused here rather than uploaded and refused there.
	dropReasonTooLarge = "too_large"
	// dropReasonObjectUnreadable — the attachment row points at something this
	// deployment's storage does not hold or could not read.
	dropReasonObjectUnreadable = "object_unreadable"
	// dropReasonUploadRefused — the upload never minted a key, so no message
	// was ever addressed to the chat.
	dropReasonUploadRefused = "upload_refused"
	// dropReasonSendRefused — Feishu answered the send and refused it.
	dropReasonSendRefused = "send_refused"
	// dropReasonNotAdmitted — shed for backlog after the lookup found a file.
	dropReasonNotAdmitted = "not_admitted"
	// dropReasonLookupFailed — the attachment lookup itself failed, so whether
	// this turn carried a file is unknown; what IS known is that nothing was
	// sent.
	dropReasonLookupFailed = "lookup_failed"
	// dropReasonInterrupted — the budget ran out before a slot came free, which
	// is provably before anything was sent.
	dropReasonInterrupted = "interrupted"

	// unconfirmedReasonSendUnconfirmed — the send request failed in a way that
	// says nothing about whether Feishu processed it.
	unconfirmedReasonSendUnconfirmed = "send_unconfirmed"
)

// deliveryState is what we actually know about one file after trying to send
// it. Three values, because the two-valued version is wrong in both directions
// at once: a send whose verdict never came would be reported as a definite
// failure even though the file may well be sitting in the chat, and the local
// failures that never reached Feishu at all would be reported to nobody.
type deliveryState int

const (
	// deliveryDelivered — Feishu returned a message_id. The only state that
	// needs no message to the user; the file is what they see.
	deliveryDelivered deliveryState = iota

	// deliveryDefinitelyFailed — nothing arrived and nothing can have. Either
	// the file never became a key (the object could not be read, or the upload
	// was refused), or the send itself came back refused. Safe to describe as a
	// failure.
	deliveryDefinitelyFailed

	// deliveryUnknown — the send request went out and no verdict came back. The
	// message may be in the chat. This state must never be retried: the same
	// key sent twice shows the person the file twice and there is nothing to
	// undo it with.
	deliveryUnknown
)

// String names the states for the log, in the vocabulary the code reasons in,
// so an operator reading a line can tell an unconfirmed send from a refused one
// without knowing which Feishu code meant which.
func (d deliveryState) String() string {
	switch d {
	case deliveryDelivered:
		return "delivered"
	case deliveryDefinitelyFailed:
		return "definitely_failed"
	case deliveryUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// errAttachmentTooLarge is the local refusal for bytes past Feishu's file
// ceiling.
var errAttachmentTooLarge = errors.New("lark: attachment exceeds the outbound file limit")

// attachmentTarget is where one answer's files are going. The credentials and
// the binding are both carried because the delivery outlives the event handler
// that resolved them: the chat id and the reply target are derived from the
// binding exactly as the text reply derives them, so words and files land in
// the same 话题.
type attachmentTarget struct {
	Creds   InstallationCredentials
	Binding ChatSessionBinding
}

// PatcherOption configures the outbound Patcher at construction.
type PatcherOption func(*Patcher)

// WithAttachments turns on file delivery. Without it — a deployment with no
// object storage — an answer is delivered exactly as it was before, and the
// agent is told as much in its brief. The router wires this and the capability
// declaration under the same condition, which is what keeps the brief honest.
func WithAttachments(objects mediaObjectStore) PatcherOption {
	return func(p *Patcher) { p.objects = objects }
}

// WithOutboundMetrics wires the observability sink. Nil is fine; see
// orNopMetrics.
func WithOutboundMetrics(m Metrics) PatcherOption {
	return func(p *Patcher) { p.metrics = orNopMetrics(m) }
}

// mayCarryAttachments reports whether this turn is worth the lookups even
// though the agent may have said nothing. Everything it checks is already in
// hand, so a deployment with no storage — or an event naming no message —
// costs no query.
func (p *Patcher) mayCarryAttachments(e events.Event) bool {
	return p.objects != nil && e.WorkspaceID != "" && chatDoneMessageID(e.Payload) != ""
}

// deliverAttachments hands the answer's files to a goroutine of their own. It
// is called after the words are out, once mayCarryAttachments has said this
// turn is worth the lookup, and returns immediately.
func (p *Patcher) deliverAttachments(e events.Event, to attachmentTarget) {
	messageID, err := util.ParseUUID(chatDoneMessageID(e.Payload))
	if err != nil || !messageID.Valid {
		return // a turn with no assistant message has nothing bound to it
	}
	workspaceID, err := util.ParseUUID(e.WorkspaceID)
	if err != nil || !workspaceID.Valid {
		return
	}
	if outboundChatID(to.Binding) == "" {
		return
	}
	// Admission is claimed here rather than inside the goroutine, because a
	// goroutine that has already started is a goroutine this cap did not bound.
	// The lookup it runs is on the far side of this gate too: under a slow
	// database, unbounded lookups are the same failure as unbounded goroutines
	// wearing a different hat.
	//
	// Nothing is known about this turn yet — whether a file is bound to it is
	// exactly what the lookup would tell us — so a refusal here is logged and
	// not spoken. Telling the user their file was dropped when the turn may have
	// carried none is the false alarm the post-lookup gate exists to avoid.
	if !p.admitAttachmentDelivery() {
		// A scheduling refusal, counted under its own unit, and nothing else:
		// this gate runs before the lookup, so it knows neither how many files
		// the turn carries nor whether it carries any at all. A per-file counter
		// fed from here would fabricate cardinality.
		p.metrics.RecordAttachmentDeliveryShed()
		p.cfg.Logger.Warn("lark outbound: attachment delivery not admitted, too many already running",
			"app_id", to.Creds.AppID, "admitted", maxAdmittedAttachmentDeliveries)
		return
	}
	p.spawn(func() {
		defer p.releaseAttachmentAdmission()
		// A context of its own: the handler's 10s budget is sized for a text
		// reply, and it is already cancelled by the time this runs.
		ctx, cancel := context.WithTimeout(context.Background(), attachmentBudget)
		defer cancel()
		p.sendAttachments(ctx, messageID, workspaceID, to)
	})
}

// sendAttachments delivers every file bound to one answer. Files are
// independent: one that fails does not stop the rest, and what is known about
// the ones that did not plainly arrive is said once at the end rather than once
// each.
//
// The caller has already claimed admission, which is what bounds the number of
// goroutines running this and the number of lookups below. What is rationed
// here is different and deliberately after the lookup: a turn with no file
// bound to it must not consume a pending slot, and — the reason this matters to
// the user rather than to the scheduler — a delivery refused for want of one
// can only be reported honestly by something that already knows a file was
// waiting.
func (p *Patcher) sendAttachments(ctx context.Context, messageID, workspaceID pgtype.UUID, to attachmentTarget) {
	rows, err := p.queries.ListAttachmentsByChatMessage(ctx, db.ListAttachmentsByChatMessageParams{
		ChatMessageID: messageID,
		WorkspaceID:   workspaceID,
	})
	if err != nil {
		p.cfg.Logger.WarnContext(ctx, "lark outbound: attachment lookup failed",
			"error", err, "chat_message_id", uuidString(messageID))
		p.metrics.RecordAttachmentDropped(dropReasonLookupFailed)
		p.tellUser(ctx, to, mediaLookupFailedText)
		return
	}
	if len(rows) == 0 {
		// mayCarryAttachments said there might be one; there was not.
		return
	}
	// Past here a file is known to be waiting, so every way out of this function
	// has to end in either a delivery or a sentence to the user.

	// Shed when too many deliveries that found a file are already outstanding.
	// The semaphore below bounds how many RUN at once; this bounds how many wait
	// for it, and unlike admission it can name what was dropped, so the user
	// hears about it.
	if !p.claimAttachmentSlot() {
		for range rows {
			p.metrics.RecordAttachmentDropped(dropReasonNotAdmitted)
		}
		p.cfg.Logger.WarnContext(ctx, "lark outbound: attachment delivery shed, too many already pending",
			"app_id", to.Creds.AppID, "attachments", len(rows),
			"pending", maxPendingAttachmentDeliveries)
		p.tellUser(ctx, to, mediaSendFailedText)
		return
	}
	defer p.releaseAttachmentSlot()

	// Acquired here and never before the spawn. Bus.Publish is synchronous on
	// the task-completion goroutine, so blocking out there would wedge the
	// completion path for up to the attachment budget — the very thing the spawn
	// exists to prevent.
	select {
	case attachmentSlots <- struct{}{}:
		defer func() { <-attachmentSlots }()
	case <-ctx.Done():
		// The rows are known here, so every one of them settles: nothing was
		// sent, which is the one case that is provably local.
		for range rows {
			p.metrics.RecordAttachmentDropped(dropReasonInterrupted)
		}
		p.cfg.Logger.WarnContext(ctx, "lark outbound: attachment delivery gave up waiting for a slot",
			"app_id", to.Creds.AppID, "attachments", len(rows))
		// Deliberately on a fresh context: the one that expired is the reason we
		// are here, and reusing it would drop the sentence too.
		p.tellUser(context.WithoutCancel(ctx), to, mediaSendFailedText)
		return
	}

	failed, unknown := 0, 0
	for _, row := range rows {
		state, reason, err := p.sendAttachment(ctx, row, to)
		switch state {
		case deliveryDefinitelyFailed:
			failed++
			p.metrics.RecordAttachmentDropped(reason)
		case deliveryUnknown:
			// Not a failure: the request may well have been processed. Filed
			// under its own counter so the drop rate stays a rate of definite
			// drops.
			unknown++
			p.metrics.RecordAttachmentUnconfirmed(reason)
		default:
			p.metrics.RecordAttachmentDelivered()
		}
		if err != nil {
			// The object's URL stays out of the log: it is an address that
			// serves the file to whoever holds it.
			p.cfg.Logger.WarnContext(ctx, "lark outbound: attachment not confirmed delivered",
				"error", err,
				"delivery", state.String(),
				"reason", reason,
				"app_id", to.Creds.AppID,
				"attachment_id", uuidString(row.ID),
				"content_type", row.ContentType,
				"size_bytes", row.SizeBytes)
		}
	}
	// The answer may already be on the user's screen and may well refer to a
	// file. Saying nothing would leave them looking for one that never comes —
	// but saying "it failed" about a file that did arrive is its own harm, so
	// each group speaks for itself and an unconfirmed send never borrows the
	// definite wording.
	var lines []string
	if failed > 0 {
		lines = append(lines, mediaSendFailedText)
	}
	if unknown > 0 {
		lines = append(lines, mediaSendUnknownText)
	}
	if len(lines) > 0 {
		p.tellUser(ctx, to, strings.Join(lines, "\n"))
	}
}

// tellUser puts one sentence into the conversation, best effort. Every caller
// is already on a path where something went wrong, so a failure here is logged
// and dropped rather than propagated — there is nothing further to try.
func (p *Patcher) tellUser(ctx context.Context, to attachmentTarget, text string) {
	err := sendWithThreadFallback(p.cfg.Logger, "send attachment notice", threadReplyTarget(to.Binding), func(t ReplyTarget) error {
		_, err := p.client.SendTextMessage(ctx, SendTextParams{
			InstallationID: to.Creds,
			ChatID:         outboundChatID(to.Binding),
			Text:           text,
			ReplyTarget:    t,
		})
		return err
	})
	if err != nil {
		p.cfg.Logger.WarnContext(ctx, "lark outbound: could not tell the user about the file",
			"error", err, "app_id", to.Creds.AppID)
	}
}

// sendAttachment carries one file from object storage into the chat, and reports
// what is known about where it ended up. The error is for the log; the state
// and the reason are what the user and the operator are told.
func (p *Patcher) sendAttachment(ctx context.Context, row db.Attachment, to attachmentTarget) (deliveryState, string, error) {
	// The recorded size is checked before a single byte is fetched. An oversize
	// attachment is refused either way — readObject re-checks what it actually
	// read, because the column is metadata and the object is the truth — but
	// reading 40 MiB out of storage to then refuse it is work nobody benefits
	// from.
	if row.SizeBytes > maxOutboundFileBytes {
		return deliveryDefinitelyFailed, dropReasonTooLarge,
			fmt.Errorf("attachment is %d bytes: %w", row.SizeBytes, errAttachmentTooLarge)
	}
	data, err := p.readObject(ctx, row.Url)
	if err != nil {
		if errors.Is(err, errAttachmentTooLarge) {
			return deliveryDefinitelyFailed, dropReasonTooLarge, err
		}
		return deliveryDefinitelyFailed, dropReasonObjectUnreadable, err
	}
	name := outboundAttachmentName(row.Filename, row.ContentType)
	target := threadReplyTarget(to.Binding)

	// The kind decision has to be made before the upload: Feishu's two upload
	// endpoints mint keys in two namespaces and a file_key in an image message
	// is refused.
	if feishuSendsAsImage(row.ContentType, name, len(data)) {
		key, err := p.client.UploadImage(ctx, UploadImageParams{
			InstallationID: to.Creds, Filename: name, Data: data,
		})
		if err != nil {
			// A failed upload never produced an image_key, so no message was
			// ever addressed to the chat. The file is definitely not there.
			return deliveryDefinitelyFailed, dropReasonUploadRefused, fmt.Errorf("upload image: %w", err)
		}
		err = sendWithThreadFallback(p.cfg.Logger, "send image message", target, func(t ReplyTarget) error {
			_, sendErr := p.client.SendImageMessage(ctx, SendImageParams{
				InstallationID: to.Creds,
				ChatID:         outboundChatID(to.Binding),
				ImageKey:       key,
				ReplyTarget:    t,
			})
			return sendErr
		})
		state, reason := sendOutcome(err)
		return state, reason, err
	}

	key, err := p.client.UploadFile(ctx, UploadFileParams{
		InstallationID: to.Creds, FileType: feishuFileType(name), Filename: name, Data: data,
	})
	if err != nil {
		return deliveryDefinitelyFailed, dropReasonUploadRefused, fmt.Errorf("upload file: %w", err)
	}
	err = sendWithThreadFallback(p.cfg.Logger, "send file message", target, func(t ReplyTarget) error {
		_, sendErr := p.client.SendFileMessage(ctx, SendFileParams{
			InstallationID: to.Creds,
			ChatID:         outboundChatID(to.Binding),
			FileKey:        key,
			ReplyTarget:    t,
		})
		return sendErr
	})
	state, reason := sendOutcome(err)
	return state, reason, err
}

// sendOutcome reads a send's error for what it says about the message.
//
// The one distinction that matters: a refusal is Feishu answering, and a
// missing answer is not an answer.
//
//   - *APIError is a 2xx body carrying a non-zero business code — Feishu
//     processed the request and declined it. Definitive.
//
//   - *larkAPIStatusError at 4xx (other than 429) is the same verdict at the
//     HTTP layer: a malformed request or a permission the bot does not have
//     will read identically on every retry. 429 and 5xx are not verdicts about
//     this message, and a proxy's 502 may sit in front of a Feishu that
//     already accepted it.
//
//   - Everything else — a dial failure, a response body that never finished, a
//     context that ended — leaves the message possibly delivered. Reading
//     these as unknown is the direction that costs least: an unknown is never
//     resent and is described in words that hold either way, so a send that
//     never happened is under-claimed rather than a send that did happen being
//     denied.
func sendOutcome(err error) (deliveryState, string) {
	if err == nil {
		return deliveryDelivered, ""
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return deliveryDefinitelyFailed, dropReasonSendRefused
	}
	var statusErr *larkAPIStatusError
	if errors.As(err, &statusErr) {
		if statusErr.StatusCode >= 400 && statusErr.StatusCode < 500 && statusErr.StatusCode != 429 {
			return deliveryDefinitelyFailed, dropReasonSendRefused
		}
	}
	return deliveryUnknown, unconfirmedReasonSendUnconfirmed
}

// readObject pulls the whole file into memory. It has to be whole: the
// multipart upload body is replayed once after a token rejection, so it cannot
// be a stream that the first attempt consumes.
func (p *Patcher) readObject(ctx context.Context, rawURL string) ([]byte, error) {
	key := p.objects.KeyFromURL(rawURL)
	if key == "" {
		return nil, errors.New("lark: attachment is not an object this deployment stores")
	}
	rc, err := p.objects.GetReader(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	defer rc.Close()
	// One byte of headroom, so reading exactly the cap can be told from a file
	// that has more to come.
	data, err := io.ReadAll(io.LimitReader(rc, maxOutboundFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if len(data) > maxOutboundFileBytes {
		return nil, errAttachmentTooLarge
	}
	return data, nil
}

// feishuSendsAsImage decides which of Feishu's two media paths a file takes.
//
// The content type leads, because it is what the uploader declared. When it
// says nothing useful — empty, or the octet-stream that means "bytes" — the
// filename's extension is the better guess. An image past the image ceiling is
// demoted to a file rather than uploaded and refused.
func feishuSendsAsImage(contentType, filename string, size int) bool {
	ct := baseContentType(contentType)
	if isGenericBinaryContentType(ct) {
		ct = baseContentType(mime.TypeByExtension(path.Ext(filename)))
	}
	return strings.HasPrefix(ct, "image/") && size <= maxOutboundImageBytes
}

// baseContentType drops the parameters a content type may carry, so
// "text/csv; charset=utf-8" compares as "text/csv".
func baseContentType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if semi := strings.IndexByte(s, ';'); semi >= 0 {
		s = strings.TrimSpace(s[:semi])
	}
	return s
}

// feishuFileType maps a filename onto Feishu's file_type enum. The enum is
// closed — opus, mp4, pdf, doc, xls, ppt — and everything else is stream.
//
// Keyed off the extension rather than the content type because that is the
// granularity the enum has: doc and docx are one value, and a content type
// would have to be mapped back through the extension anyway. A wrong guess is
// not a failure — stream is always accepted, it just means Feishu shows a
// generic file icon instead of a Word one.
func feishuFileType(filename string) string {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(filename), ".")) {
	case "opus":
		return "opus"
	case "mp4":
		return "mp4"
	case "pdf":
		return "pdf"
	case "doc", "docx":
		return "doc"
	case "xls", "xlsx":
		return "xls"
	case "ppt", "pptx":
		return "ppt"
	default:
		return feishuFileTypeStream
	}
}

// outboundAttachmentName is what the recipient sees on the file card. It is
// reduced to a single path segment — the name reaches the wire and a stored
// filename is not guaranteed to be one — and given an extension when it has
// none, since that is the only hint Feishu gets about the format.
func outboundAttachmentName(filename, contentType string) string {
	name := cleanFilename(filename)
	if name == "" {
		name = "attachment"
	}
	if path.Ext(name) == "" {
		name += outboundNameExtension(baseContentType(contentType))
	}
	return name
}

// outboundNameExtension is what gets appended to a stored name that has none.
//
// Deliberately narrower than mediaExtension: that one falls back to the host's
// mime database, which answers text/plain with ".asc" — PGP armor. A wrong
// extension on the recipient's download is worse than no extension, so only
// the types with a pinned answer are named, and everything else keeps the name
// it was stored under.
func outboundNameExtension(contentType string) string {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp",
		"video/mp4", "audio/opus", "audio/ogg", "audio/amr", "audio/mpeg",
		"application/pdf":
		return mediaExtension(contentType)
	}
	return ""
}

// chatDoneMessageID pulls the assistant message id out of a chat:done payload
// (the typed payload, or its map form after a serialization round trip). It is
// the key every attachment on this turn is bound to.
func chatDoneMessageID(payload any) string {
	switch pl := payload.(type) {
	case protocol.ChatDonePayload:
		return pl.MessageID
	case map[string]any:
		if s, ok := pl["message_id"].(string); ok {
			return s
		}
	}
	return ""
}

// attachmentSlots caps how many attachment deliveries hold an object at once.
//
// Process-wide, not per installation: the heap is process-wide, and a
// per-installation cap on a deployment running several bots just multiplies.
// Each delivery holds one object in memory while it uploads it, so this is the
// number that decides peak resident attachment bytes.
var attachmentSlots = make(chan struct{}, maxConcurrentAttachmentDeliveries)

const (
	// maxConcurrentAttachmentDeliveries is how many objects may be in flight.
	// Small on purpose: each one is up to the 30 MiB file ceiling, held whole
	// in memory because the upload body has to be replayable.
	maxConcurrentAttachmentDeliveries = 2

	// maxPendingAttachmentDeliveries bounds the deliveries that have found a
	// file and are waiting for a slot. Past it a delivery is shed and the user
	// is told: the answer's text has already reached them, the attachment is
	// still in object storage, and silence would leave them waiting for a file
	// that is not coming.
	maxPendingAttachmentDeliveries = 32

	// maxAdmittedAttachmentDeliveries bounds the goroutines themselves, and
	// with them the attachment lookups they run before anything about the turn
	// is known. Twice the pending cap as headroom, not as a derived quantity: a
	// turn holds admission for its whole life but claims a pending slot only
	// once its lookup has found a file, so a backlog of file-carrying turns
	// meets the pending cap first — the ordering that keeps the user-facing
	// shed on the path that can name a real file.
	maxAdmittedAttachmentDeliveries = 2 * maxPendingAttachmentDeliveries
)

// claimAttachmentSlot reserves one of the pending slots, or reports that the
// backlog is full.
func (p *Patcher) claimAttachmentSlot() bool {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	if p.pendingAttachments >= maxPendingAttachmentDeliveries {
		return false
	}
	p.pendingAttachments++
	return true
}

func (p *Patcher) releaseAttachmentSlot() {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	p.pendingAttachments--
}

// admitAttachmentDelivery reserves the right to start one delivery goroutine,
// or reports that too many are already running. Claimed before the spawn: after
// it, the goroutine and its lookup are already past anything this could bound.
func (p *Patcher) admitAttachmentDelivery() bool {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	if p.admittedAttachments >= maxAdmittedAttachmentDeliveries {
		return false
	}
	p.admittedAttachments++
	return true
}

func (p *Patcher) releaseAttachmentAdmission() {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	p.admittedAttachments--
}
