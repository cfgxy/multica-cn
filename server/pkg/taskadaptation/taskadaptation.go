package taskadaptation

import (
	"encoding/json"
	"strings"
	"unicode"
)

const contextKey = "capability_adaptation"

type Snapshot struct {
	PolicyID          string   `json:"policy_id"`
	PolicyVersion     string   `json:"policy_version"`
	Scope             string   `json:"scope"`
	Trigger           string   `json:"trigger"`
	CurrentStrategy   string   `json:"current_strategy"`
	SuggestedStrategy string   `json:"suggested_strategy"`
	AllowedActions    []string `json:"allowed_actions"`
}

type Outcome struct {
	PolicyID string `json:"policy_id"`
	Status   string `json:"status"`
}

func SnapshotFromContext(raw json.RawMessage) *Snapshot {
	if len(raw) == 0 {
		return nil
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil
	}
	payload := envelope[contextKey]
	if len(payload) == 0 {
		return nil
	}
	var snapshot Snapshot
	if json.Unmarshal(payload, &snapshot) != nil || !snapshot.Valid() {
		return nil
	}
	return &snapshot
}

func (s Snapshot) Valid() bool {
	if !validText(s.PolicyID, 100) || !validText(s.PolicyVersion, 80) || s.Scope != "current_task" ||
		!validText(s.Trigger, 240) || !validText(s.CurrentStrategy, 500) ||
		!validText(s.SuggestedStrategy, 500) || len(s.AllowedActions) == 0 || len(s.AllowedActions) > 8 {
		return false
	}
	for _, action := range s.AllowedActions {
		if !validText(action, 120) {
			return false
		}
	}
	return true
}

func validText(value string, max int) bool {
	if len(value) == 0 || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func ValidStatus(status string) bool {
	switch status {
	case "applied", "retained", "blocked":
		return true
	default:
		return false
	}
}

const receiptPrefix = "<!-- multica-capability-adaptation: "
const receiptSuffix = " -->"

// ExtractReceipt removes one exact, final-line receipt only when adaptation is enabled.
func ExtractReceipt(output string, snapshot *Snapshot) (string, *Outcome) {
	if snapshot == nil || !snapshot.Valid() {
		return output, nil
	}
	trimmed := strings.TrimRight(output, "\r\n")
	lineStart := strings.LastIndex(trimmed, "\n") + 1
	line := strings.TrimSuffix(trimmed[lineStart:], "\r")
	if strings.Count(output, receiptPrefix) != 1 || !strings.HasPrefix(line, receiptPrefix) || !strings.HasSuffix(line, receiptSuffix) {
		return output, nil
	}
	status := strings.TrimSuffix(strings.TrimPrefix(line, receiptPrefix), receiptSuffix)
	if !ValidStatus(status) {
		return output, nil
	}
	cleaned := strings.TrimRight(trimmed[:lineStart], "\r\n")
	return cleaned, &Outcome{PolicyID: snapshot.PolicyID, Status: status}
}
