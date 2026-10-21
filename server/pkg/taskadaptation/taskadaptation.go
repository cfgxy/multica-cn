package taskadaptation

import (
	"encoding/json"
	"strings"
	"unicode"
)

const contextKey = "capability_adaptation"

type Snapshot struct {
	TaskID            string   `json:"task_id"`
	PolicyID          string   `json:"policy_id"`
	PolicyVersion     string   `json:"policy_version"`
	Scope             string   `json:"scope"`
	Trigger           string   `json:"trigger"`
	CurrentStrategy   string   `json:"current_strategy"`
	SuggestedStrategy string   `json:"suggested_strategy"`
	AllowedActions    []string `json:"allowed_actions"`
}

type Outcome struct {
	TaskID        string `json:"task_id"`
	PolicyID      string `json:"policy_id"`
	PolicyVersion string `json:"policy_version"`
	Status        string `json:"status"`
}

func (o Outcome) Matches(snapshot *Snapshot, taskID string) bool {
	return snapshot != nil && snapshot.Valid() && taskID != "" && snapshot.TaskID == taskID && o.TaskID == taskID && o.PolicyID == snapshot.PolicyID &&
		o.PolicyVersion == snapshot.PolicyVersion && ValidStatus(o.Status)
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
const receiptMarker = "multica-capability-adaptation:"

// StripReceiptLines removes internal adaptation marker lines from user-visible output.
func StripReceiptLines(output string) string {
	parts := strings.SplitAfter(output, "\n")
	var clean strings.Builder
	stripped := false
	for _, part := range parts {
		line := strings.TrimSuffix(strings.TrimSuffix(part, "\n"), "\r")
		if strings.Contains(line, receiptMarker) {
			stripped = true
			continue
		}
		clean.WriteString(part)
	}
	result := clean.String()
	if stripped {
		return strings.TrimRight(result, "\r\n")
	}
	return result
}

// ExtractReceipt always strips internal receipt lines from user-visible output.
// Only one exact final receipt bound to the claimed task can produce an outcome.
func ExtractReceipt(output string, snapshot *Snapshot, taskID string) (string, *Outcome) {
	parts := strings.SplitAfter(output, "\n")
	var receipt string
	var receiptIndex = -1
	count := 0
	for i, part := range parts {
		line := strings.TrimSuffix(strings.TrimSuffix(part, "\n"), "\r")
		if strings.Contains(line, receiptMarker) {
			count++
			receipt = line
			receiptIndex = i
		}
	}
	cleaned := StripReceiptLines(output)
	if count != 1 || snapshot == nil || !snapshot.Valid() || taskID == "" || snapshot.TaskID != taskID {
		return cleaned, nil
	}
	lastContent := len(parts) - 1
	for lastContent >= 0 && strings.Trim(parts[lastContent], "\r\n") == "" {
		lastContent--
	}
	if receiptIndex != lastContent || !strings.HasPrefix(receipt, receiptPrefix) || !strings.HasSuffix(receipt, receiptSuffix) {
		return cleaned, nil
	}
	value := strings.TrimSuffix(strings.TrimPrefix(receipt, receiptPrefix), receiptSuffix)
	separator := strings.LastIndex(value, ": ")
	if separator <= 0 || value[:separator] != taskID {
		return cleaned, nil
	}
	status := value[separator+2:]
	if !ValidStatus(status) {
		return cleaned, nil
	}
	return strings.TrimRight(cleaned, "\r\n"), &Outcome{TaskID: taskID, PolicyID: snapshot.PolicyID, PolicyVersion: snapshot.PolicyVersion, Status: status}
}
