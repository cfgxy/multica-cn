package lark

import (
	"encoding/json"
	"strings"

	engine "github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

// renderHelpCard produces the interactive command card posted on /help
// (RUYI-461): one line of description per listed command plus a click-
// to-dispatch button for each.
//
// Every button value mints {"v":1,"cmd":<name>,"ct":<chatType>}. The
// card.action.trigger context carries no chat_type, so the click's
// routing attribute is frozen here at render time; on click the WS frame
// decoder re-validates v / ct / cmd against the shared command registry
// and synthesizes a body of the bare command token — which then flows
// through the exact ParseControlCommand path a typed command takes. Only
// Listed commands appear; the registry owns that decision, not this
// renderer.
func renderHelpCard(header string, cmds []engine.CommandDescriptor, chatType ChatType) (string, error) {
	lines := make([]string, 0, len(cmds))
	buttons := make([]any, 0, len(cmds))
	for _, c := range cmds {
		lines = append(lines, "**"+c.Name+"** — "+c.Summary)
		buttons = append(buttons, map[string]any{
			"tag":   "button",
			"text":  map[string]any{"tag": "plain_text", "content": c.Name},
			"type":  "default",
			"value": map[string]any{"v": 1, "cmd": c.Name, "ct": string(chatType)},
		})
	}
	doc := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": header},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": strings.Join(lines, "\n"),
				},
			},
			map[string]any{"tag": "hr"},
			map[string]any{"tag": "action", "actions": buttons},
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "plain_text",
					"content": "也可以在输入框直接输入命令；发送 /help 随时查看本卡片。",
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
