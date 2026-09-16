package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareClaudeSkillSettings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path, err := prepareClaudeSkillSettings(root, []RuntimeSkillRefForEnv{
		{Root: "provider", Key: "review-dir", Name: "review"},
		{Root: "plugin", Key: "paper:design-to-code", Plugin: "paper@market"},
	}, nil, false)
	if err != nil {
		t.Fatalf("prepareClaudeSkillSettings: %v", err)
	}
	if path == "" {
		t.Fatal("expected task-local settings path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var got struct {
		SkillOverrides map[string]string `json:"skillOverrides"`
		Permissions    struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got.SkillOverrides["review"] != "off" {
		t.Fatalf("ordinary skill override = %q, want off", got.SkillOverrides["review"])
	}
	if _, exists := got.SkillOverrides["paper:design-to-code"]; exists {
		t.Fatal("plugin skills must not use Claude's unsupported skillOverrides path")
	}
	for _, want := range []string{
		"Skill(review)",
		"Skill(review *)",
		"Skill(paper:design-to-code)",
		"Skill(paper:design-to-code *)",
	} {
		found := false
		for _, rule := range got.Permissions.Deny {
			found = found || rule == want
		}
		if !found {
			t.Errorf("missing deny rule %q in %v", want, got.Permissions.Deny)
		}
	}

	cleared, err := prepareClaudeSkillSettings(root, nil, nil, true)
	if err != nil {
		t.Fatalf("clear settings: %v", err)
	}
	if cleared != "" {
		t.Fatalf("cleared settings path = %q, want empty", cleared)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale settings file still exists: %v", err)
	}
}

func TestPrepareClaudeSkillSettingsDeniesSubagentToolsByDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// No disabled skills at all — the tool deny alone must still produce the
	// settings file, otherwise a skill-less agent would silently regain the
	// task-delegation tools.
	path, err := prepareClaudeSkillSettings(root, nil, nil, false)
	if err != nil {
		t.Fatalf("prepareClaudeSkillSettings: %v", err)
	}
	if path == "" {
		t.Fatal("expected settings file with subagent tool deny")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var got struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if len(got.Permissions.Deny) != 2 {
		t.Fatalf("deny rules = %v, want exactly Agent+Task", got.Permissions.Deny)
	}
	if got.Permissions.Deny[0] != "Agent" || got.Permissions.Deny[1] != "Task" {
		t.Fatalf("deny rules = %v, want [Agent Task]", got.Permissions.Deny)
	}
}

func TestPrepareClaudeSkillSettingsAllowsSubagentToolsWhenOptedIn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path, err := prepareClaudeSkillSettings(root, nil, nil, true)
	if err != nil {
		t.Fatalf("prepareClaudeSkillSettings: %v", err)
	}
	if path != "" {
		t.Fatalf("allow-listed agent should have no settings file, got %q", path)
	}

	// Skill filtering must keep working alongside the opt-in.
	path, err = prepareClaudeSkillSettings(root, []RuntimeSkillRefForEnv{
		{Root: "provider", Key: "review-dir", Name: "review"},
	}, nil, true)
	if err != nil {
		t.Fatalf("prepareClaudeSkillSettings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(data), `"Agent"`) {
		t.Fatalf("opted-in agent must not deny subagent tools:\n%s", data)
	}
}

func TestSubagentToolsAllowed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{"empty config", nil, false},
		{"empty object", json.RawMessage("{}"), false},
		{"explicit false", json.RawMessage(`{"allow_subagents": false}`), false},
		{"explicit true", json.RawMessage(`{"allow_subagents": true}`), true},
		{"malformed", json.RawMessage(`{invalid`), false},
		{"provider fields ignored", json.RawMessage(`{"mode":"gateway","allow_subagents":true}`), true},
	}
	for _, tc := range cases {
		if got := SubagentToolsAllowed(tc.raw); got != tc.want {
			t.Errorf("%s: SubagentToolsAllowed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMaxTurnsFromRuntimeConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  json.RawMessage
		want int
	}{
		{"empty config", nil, 0},
		{"empty object", json.RawMessage("{}"), 0},
		{"positive", json.RawMessage(`{"max_turns": 400}`), 400},
		{"negative treated as off", json.RawMessage(`{"max_turns": -5}`), 0},
		{"malformed", json.RawMessage(`{invalid`), 0},
		{"non-integer", json.RawMessage(`{"max_turns": "400"}`), 0},
		{"other keys ignored", json.RawMessage(`{"allow_subagents":true,"max_turns":250}`), 250},
	}
	for _, tc := range cases {
		if got := MaxTurnsFromRuntimeConfig(tc.raw); got != tc.want {
			t.Errorf("%s: MaxTurnsFromRuntimeConfig = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestMaxContextTokensFromRuntimeConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  json.RawMessage
		want int64
	}{
		{"empty config → off (model-related, user sets)", nil, 0},
		{"absent field → off", json.RawMessage(`{"max_turns":400}`), 0},
		{"explicit zero disables", json.RawMessage(`{"max_context_tokens":0}`), 0},
		{"custom ceiling", json.RawMessage(`{"max_context_tokens":300000}`), 300_000},
		{"below floor floors", json.RawMessage(`{"max_context_tokens":50000}`), 100_000},
		{"malformed → off", json.RawMessage(`{invalid`), 0},
	}
	for _, tc := range cases {
		if got := MaxContextTokensFromRuntimeConfig(tc.raw); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEnsureCodexDisabledSkillsConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureCodexDisabledSkillsConfig(configPath, root, []RuntimeSkillRefForEnv{
		{Root: "provider", Key: "review"},
		{Root: "universal", Key: "shared/release"},
		{Root: "provider", Key: "../escape"},
	}, nil); err != nil {
		t.Fatalf("ensureCodexDisabledSkillsConfig: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Count(content, "[[skills.config]]") != 2 {
		t.Fatalf("disabled entry count mismatch:\n%s", content)
	}
	wantProvider := filepath.ToSlash(filepath.Join(root, "skills", "review", "SKILL.md"))
	if !strings.Contains(content, wantProvider) {
		t.Fatalf("missing provider skill path %q:\n%s", wantProvider, content)
	}
	if strings.Contains(content, "escape") {
		t.Fatalf("unsafe key leaked into config:\n%s", content)
	}
}

func TestRuntimeSkillPoliciesYieldToWorkspaceSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspaceSkills := []SkillContextForEnv{{Name: "Review"}}
	settingsPath, err := prepareClaudeSkillSettings(root, []RuntimeSkillRefForEnv{
		{Root: "provider", Key: "review-dir", Name: "review"},
	}, workspaceSkills, true)
	if err != nil {
		t.Fatalf("prepareClaudeSkillSettings: %v", err)
	}
	if settingsPath != "" {
		t.Fatalf("workspace-owned Claude skill was disabled via %q", settingsPath)
	}

	configPath := filepath.Join(root, "config.toml")
	if err := ensureCodexDisabledSkillsConfig(configPath, root, []RuntimeSkillRefForEnv{
		{Root: "provider", Key: "review"},
	}, workspaceSkills); err != nil {
		t.Fatalf("ensureCodexDisabledSkillsConfig: %v", err)
	}
	if data, err := os.ReadFile(configPath); err == nil && strings.Contains(string(data), "[[skills.config]]") {
		t.Fatalf("workspace-owned Codex skill was disabled:\n%s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
