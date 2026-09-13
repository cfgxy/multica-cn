package execenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const claudeRuntimeSkillSettingsFile = "claude-runtime-skill-settings.json"

// subagentDenyTools lists the provider-native task-delegation tool names that
// are denied unless the agent's runtime_config explicitly allows subagents
// (runtime_config.allow_subagents). Both spellings are emitted because the
// same deny list serves every CLI flavor backed by this settings file.
var subagentDenyTools = []string{"Agent", "Task"}

// RuntimeSkillRefForEnv identifies a runtime-local skill for provider-specific
// task environment filtering. Provider and runtime are already selected by the
// task, so only the discovery root and provider-native key are needed here.
type RuntimeSkillRefForEnv struct {
	Root   string
	Key    string
	Name   string
	Plugin string
}

// SubagentToolsAllowed reports whether an agent's runtime_config explicitly
// allows subagent tools. Absent or malformed config denies — the default
// posture is fail-closed: an agent must opt in to task delegation.
func SubagentToolsAllowed(runtimeConfig json.RawMessage) bool {
	if len(runtimeConfig) == 0 {
		return false
	}
	var cfg struct {
		AllowSubagents bool `json:"allow_subagents"`
	}
	if err := json.Unmarshal(runtimeConfig, &cfg); err != nil {
		return false
	}
	return cfg.AllowSubagents
}

func cleanRuntimeSkillKey(key string) (string, bool) {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(key)))
	if cleaned == "." || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(cleaned), true
}

func prepareClaudeSkillSettings(envRoot string, disabled []RuntimeSkillRefForEnv, workspaceSkills []SkillContextForEnv, allowSubagents bool) (string, error) {
	path := filepath.Join(envRoot, claudeRuntimeSkillSettingsFile)

	overrides := make(map[string]string)
	deny := make([]string, 0, len(disabled)*2+len(subagentDenyTools))
	seenDeny := make(map[string]struct{}, len(disabled)*2+len(subagentDenyTools))
	addDeny := func(rule string) {
		if _, exists := seenDeny[rule]; exists {
			return
		}
		seenDeny[rule] = struct{}{}
		deny = append(deny, rule)
	}
	if !allowSubagents {
		for _, tool := range subagentDenyTools {
			addDeny(tool)
		}
	}
	for _, skill := range disabled {
		key, ok := cleanRuntimeSkillKey(skill.Key)
		if !ok {
			continue
		}
		invocationName := strings.TrimSpace(skill.Name)
		if invocationName == "" {
			invocationName = filepath.Base(filepath.FromSlash(key))
		}
		if workspaceClaimsRuntimeSkill(invocationName, workspaceSkills) {
			continue
		}
		// Claude Code's skillOverrides fully hides personal/project skills.
		// Plugin skills ignore that setting, so the permission deny below is
		// also emitted for every key and is the enforcement path for plugins.
		if skill.Root != "plugin" {
			overrides[invocationName] = "off"
		} else {
			invocationName = key
		}
		addDeny("Skill(" + invocationName + ")")
		addDeny("Skill(" + invocationName + " *)")
	}
	if len(overrides) == 0 && len(deny) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "", nil
	}
	payload := map[string]any{
		"skillOverrides": overrides,
		"permissions": map[string]any{
			"deny": deny,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func ensureCodexDisabledSkillsConfig(configPath, codexHome string, disabled []RuntimeSkillRefForEnv, workspaceSkills []SkillContextForEnv) error {
	if len(disabled) == 0 {
		return nil
	}
	home := ""
	paths := make([]string, 0, len(disabled))
	seen := make(map[string]struct{}, len(disabled))
	for _, skill := range disabled {
		key, ok := cleanRuntimeSkillKey(skill.Key)
		if !ok {
			continue
		}
		var skillPath string
		switch skill.Root {
		case "provider":
			firstKeyPart := strings.SplitN(key, "/", 2)[0]
			if workspaceClaimsRuntimeSkill(firstKeyPart, workspaceSkills) {
				continue
			}
			skillPath = filepath.Join(codexHome, "skills", filepath.FromSlash(key), "SKILL.md")
		case "universal":
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("resolve user home for disabled Codex skills: %w", err)
				}
			}
			skillPath = filepath.Join(home, ".agents", "skills", filepath.FromSlash(key), "SKILL.md")
		default:
			continue
		}
		if _, exists := seen[skillPath]; exists {
			continue
		}
		seen[skillPath] = struct{}{}
		paths = append(paths, skillPath)
	}
	if len(paths) == 0 {
		return nil
	}
	file, err := os.OpenFile(configPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, path := range paths {
		block := "\n[[skills.config]]\npath = " + strconv.Quote(filepath.ToSlash(path)) + "\nenabled = false\n"
		if _, err := file.WriteString(block); err != nil {
			return err
		}
	}
	return nil
}

func workspaceClaimsRuntimeSkill(name string, workspaceSkills []SkillContextForEnv) bool {
	claim := sanitizeSkillName(name)
	for _, skill := range workspaceSkills {
		if sanitizeSkillName(skill.Name) == claim {
			return true
		}
	}
	return false
}
