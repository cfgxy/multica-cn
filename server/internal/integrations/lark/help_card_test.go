package lark

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	engine "github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// helpCardDoc is the generic shape a rendered help card unmarshals into,
// scoped to the fields the tests assert on.
type helpCardDoc struct {
	Header struct {
		Title struct {
			Content string `json:"content"`
		} `json:"title"`
	} `json:"header"`
	Elements []struct {
		Tag  string `json:"tag"`
		Text *struct {
			Content string `json:"content"`
		} `json:"text,omitempty"`
		Actions []struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
			Value struct {
				V   int    `json:"v"`
				Cmd string `json:"cmd"`
				CT  string `json:"ct"`
			} `json:"value"`
		} `json:"actions,omitempty"`
	} `json:"elements"`
}

func unmarshalHelpCard(t *testing.T, cardJSON string) helpCardDoc {
	t.Helper()
	var doc helpCardDoc
	if err := json.Unmarshal([]byte(cardJSON), &doc); err != nil {
		t.Fatalf("unmarshal help card: %v", err)
	}
	return doc
}

func TestRenderHelpCardListsRegistryCommands(t *testing.T) {
	t.Parallel()
	cmds := engine.DefaultCommandRegistry().ListedCommands()
	card, err := renderHelpCard("测试助手", cmds, ChatTypeGroup)
	if err != nil {
		t.Fatalf("renderHelpCard: %v", err)
	}
	doc := unmarshalHelpCard(t, card)

	if doc.Header.Title.Content != "测试助手" {
		t.Errorf("header title = %q", doc.Header.Title.Content)
	}

	// One action element, one button per listed command, in registration
	// order. Button values must mint v=1 + the chat type so a later click
	// passes decoder validation.
	var action *struct {
		Tag     string `json:"tag"`
		Actions []struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
			Value struct {
				V   int    `json:"v"`
				Cmd string `json:"cmd"`
				CT  string `json:"ct"`
			} `json:"value"`
		} `json:"actions,omitempty"`
	}
	var bodyText string
	for i := range doc.Elements {
		switch doc.Elements[i].Tag {
		case "action":
			// Re-encode to reuse the typed slice above.
			raw, _ := json.Marshal(doc.Elements[i])
			a := new(struct {
				Tag     string `json:"tag"`
				Actions []struct {
					Text struct {
						Content string `json:"content"`
					} `json:"text"`
					Value struct {
						V   int    `json:"v"`
						Cmd string `json:"cmd"`
						CT  string `json:"ct"`
					} `json:"value"`
				} `json:"actions,omitempty"`
			})
			if err := json.Unmarshal(raw, a); err != nil {
				t.Fatalf("re-unmarshal action: %v", err)
			}
			action = a
		case "div":
			if doc.Elements[i].Text != nil {
				bodyText += doc.Elements[i].Text.Content + "\n"
			}
		}
	}
	if action == nil {
		t.Fatal("help card has no action element")
	}
	if len(action.Actions) != len(cmds) {
		t.Fatalf("buttons = %d, want %d", len(action.Actions), len(cmds))
	}
	for i, btn := range action.Actions {
		want := cmds[i]
		if btn.Text.Content != want.Name {
			t.Errorf("button[%d] label = %q, want %q", i, btn.Text.Content, want.Name)
		}
		if btn.Value.V != 1 || btn.Value.Cmd != want.Name || btn.Value.CT != string(ChatTypeGroup) {
			t.Errorf("button[%d] value = %+v, want v=1 cmd=%s ct=group", i, btn.Value, want.Name)
		}
	}
	// The description block must name every command and its summary, and
	// the footer must teach the typed /help fallback.
	for _, c := range cmds {
		if !strings.Contains(bodyText, c.Name) || !strings.Contains(bodyText, c.Summary) {
			t.Errorf("description block missing %s / %q: %s", c.Name, c.Summary, bodyText)
		}
	}
	if !strings.Contains(bodyText, "/help") {
		t.Errorf("footer missing /help guidance: %s", bodyText)
	}
}

func TestRenderHelpCardMintsChatTypeAtRenderTime(t *testing.T) {
	t.Parallel()
	card, err := renderHelpCard("h", engine.DefaultCommandRegistry().ListedCommands(), ChatTypeP2P)
	if err != nil {
		t.Fatalf("renderHelpCard: %v", err)
	}
	if !strings.Contains(card, `"ct":"p2p"`) {
		t.Errorf("p2p card must mint ct=p2p: %s", card)
	}
	if strings.Contains(card, `"ct":"group"`) {
		t.Errorf("p2p card must not mint ct=group: %s", card)
	}
}

func newHelpTestReplier(t *testing.T, stub *stubAPIClientWithRecorder) *LarkOutcomeReplier {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rep := NewLarkOutcomeReplier(OutcomeReplierConfig{
		APIClient:   stub,
		BindingSvc:  &BindingTokenService{},
		Credentials: stubCredentialsResolver{secret: "s"},
		Queries:     stubReplierQueries{agent: db.Agent{Name: "助手"}},
		AppURL:      "https://multica.test",
		Logger:      log,
	})
	prod, ok := rep.(*LarkOutcomeReplier)
	if !ok {
		t.Fatalf("expected production replier, got %T", rep)
	}
	return prod
}

func TestLarkOutcomeReplierHelpSendsCommandCard(t *testing.T) {
	t.Parallel()
	stub := &stubAPIClientWithRecorder{configured: true}
	rep := newHelpTestReplier(t, stub)
	inst := Installation{AppID: "cli_x"}
	msg := InboundMessage{ChatID: "oc_chat", ChatType: ChatTypeP2P, CommandBody: "/help", Body: "/help"}
	rep.Reply(context.Background(), inst, msg, DispatchResult{
		Outcome:      OutcomeHelp,
		HelpCommands: engine.DefaultCommandRegistry().ListedCommands(),
	})

	if len(stub.interactiveOut) != 1 {
		t.Fatalf("expected one SendInteractiveCard call, got %d", len(stub.interactiveOut))
	}
	got := stub.interactiveOut[0]
	if got.ChatID != "oc_chat" {
		t.Errorf("ChatID = %q", got.ChatID)
	}
	if !contains(got.CardJSON, "助手") {
		t.Errorf("card should carry the agent name header: %s", got.CardJSON)
	}
	// The buttons must be minted for THIS chat type so clicks decode valid.
	if !contains(got.CardJSON, `"ct":"p2p"`) {
		t.Errorf("card buttons must mint ct=p2p: %s", got.CardJSON)
	}
}

func TestLarkOutcomeReplierUnknownCommandSendsGuidance(t *testing.T) {
	t.Parallel()
	stub := &stubAPIClientWithRecorder{configured: true}
	rep := newHelpTestReplier(t, stub)
	msg := InboundMessage{ChatID: "oc_chat", ChatType: ChatTypeGroup, CommandBody: "/stop", Body: "/stop"}
	rep.Reply(context.Background(), Installation{AppID: "cli_x"}, msg, DispatchResult{
		Outcome:      OutcomeUnknownCommand,
		CommandToken: "/stop",
	})

	if len(stub.interactiveOut) != 1 {
		t.Fatalf("expected one SendInteractiveCard call, got %d", len(stub.interactiveOut))
	}
	card := stub.interactiveOut[0].CardJSON
	if !contains(card, "/stop") {
		t.Errorf("guidance must echo the unknown token: %s", card)
	}
	if !contains(card, "/help") {
		t.Errorf("guidance must point at /help: %s", card)
	}
	if contains(card, `"tag":"button"`) {
		t.Errorf("guidance notice must not carry command buttons: %s", card)
	}
}

func TestLarkOutcomeReplierHelpEmptyCommandsDegradesToNotice(t *testing.T) {
	t.Parallel()
	stub := &stubAPIClientWithRecorder{configured: true}
	rep := newHelpTestReplier(t, stub)
	msg := InboundMessage{ChatID: "oc_chat", ChatType: ChatTypeP2P}
	rep.Reply(context.Background(), Installation{}, msg, DispatchResult{Outcome: OutcomeHelp})

	if len(stub.interactiveOut) != 1 {
		t.Fatalf("expected one fallback card, got %d", len(stub.interactiveOut))
	}
	if !contains(stub.interactiveOut[0].CardJSON, "当前没有可用的斜杠命令") {
		t.Errorf("fallback notice copy missing: %s", stub.interactiveOut[0].CardJSON)
	}
}

// TestFeishuResolverSetOptsIntoSlashFeedback pins the channel opt-in: the
// Feishu ResolverSet enables slash-command interception; the engine treats
// a disabled set as legacy ingest (covered router-side).
func TestFeishuResolverSetOptsIntoSlashFeedback(t *testing.T) {
	t.Parallel()
	set := NewFeishuResolverSet(nil, nil, nil, nil, nil, nil)
	if !set.SlashCommandFeedback {
		t.Fatal("Feishu ResolverSet must opt into SlashCommandFeedback (RUYI-461)")
	}
	if set.OriginType != originFeishuChat {
		t.Errorf("OriginType = %q", set.OriginType)
	}
}

// TestDispatchResultFromEngineCarriesCommandFields pins the engine→lark
// mapping for the two new fields; a missed mapping would render an empty
// help card and a guidance message with no token.
func TestDispatchResultFromEngineCarriesCommandFields(t *testing.T) {
	t.Parallel()
	cmds := engine.DefaultCommandRegistry().ListedCommands()
	res := dispatchResultFromEngine(engine.Result{
		Outcome:      engine.OutcomeHelp,
		CommandToken: "/stop",
		HelpCommands: cmds,
	})
	if res.Outcome != OutcomeHelp {
		t.Errorf("Outcome = %q", res.Outcome)
	}
	if res.CommandToken != "/stop" {
		t.Errorf("CommandToken = %q", res.CommandToken)
	}
	if len(res.HelpCommands) != len(cmds) {
		t.Fatalf("HelpCommands = %d, want %d", len(res.HelpCommands), len(cmds))
	}
	if res.HelpCommands[0].Name != cmds[0].Name {
		t.Errorf("HelpCommands[0] = %+v", res.HelpCommands[0])
	}
}
