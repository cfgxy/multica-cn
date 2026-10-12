package computer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const defaultFailureLimit = 5

// Linux usernames we will interpolate into paths and systemd instance names.
var usernameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

var emailRE = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// modelEnvKeys are the only keys a settings page may push into the daemon environment.
var modelEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY":  {},
	"OPENAI_API_KEY":     {},
	"OPENROUTER_API_KEY": {},
	"GEMINI_API_KEY":     {},
	"XAI_API_KEY":        {},
}

func validateUsername(name string) error {
	if !usernameRE.MatchString(name) {
		return fmt.Errorf("username must match %s", usernameRE.String())
	}
	return nil
}

// ValidateLinuxUsername is the exported form of validateUsername for callers
// outside this package that need to pre-validate before building SSH commands.
func ValidateLinuxUsername(name string) error { return validateUsername(name) }

// rejectControls refuses NUL and every other ASCII control character.
// These values are copied into NUL-separated payloads, shell stdin, and files.
func rejectControls(label, v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains a control character", label)
		}
	}
	return nil
}

func validateGit(name, email string) error {
	if len(name) > 128 {
		return fmt.Errorf("git name is empty or too long")
	}
	if err := rejectControls("git name", name); err != nil {
		return err
	}
	if err := rejectControls("git email", email); err != nil {
		return err
	}
	if email != "" && !emailRE.MatchString(email) {
		return fmt.Errorf("git email is not a single address")
	}
	return nil
}

// ValidateModelEnv accepts zero or more KEY=value lines from the allowlist.
// It rejects MULTICA_* and any other key so a settings page cannot override
// the daemon's own environment.
func ValidateModelEnv(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\x00") {
		return "", fmt.Errorf("model env contains a control character")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var b strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return "", fmt.Errorf("model env line must be KEY=value")
		}
		if strings.HasPrefix(key, "MULTICA_") {
			return "", fmt.Errorf("model env cannot set MULTICA_ variables")
		}
		if _, allowed := modelEnvKeys[key]; !allowed {
			return "", fmt.Errorf("model env key is not allowed")
		}
		if err := rejectControls("model env value", val); err != nil {
			return "", err
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(val))
		b.WriteByte('\n')
	}
	return b.String(), nil
}
