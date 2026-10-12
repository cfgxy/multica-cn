package computer

import (
	"fmt"
	"sort"
	"strings"
)

// Action is what the bastion does for one provision click.
type Action string

const (
	// ActionCreate is a new Linux user. Password comes from the settings page.
	ActionCreate Action = "create"
	// ActionReuse keeps an existing Linux user after the page password matches.
	ActionReuse Action = "reuse"
	// ActionRename means the name is taken and the password did not match.
	// The user must pick another name. Do not touch the existing account.
	ActionRename Action = "rename"
	// ActionLocked means too many failed password checks. Do not call PAM.
	ActionLocked Action = "locked"
)

// Input is the pure decision for option A from the frozen plan.
type Input struct {
	Username     string
	UserExists   bool
	PasswordOK   bool
	Failures     int
	FailureLimit int
}

// Result tells the caller what to do and why. Reason never includes secrets.
type Result struct {
	Action   Action
	Reason   string
	Failures int
}

// Decide implements: create if absent; reuse only when the page password
// matches the existing Linux password; otherwise force a rename. When
// Failures is already at FailureLimit, return locked without looking at
// the password. FailureLimit <= 0 uses defaultFailureLimit.
func Decide(in Input) (Result, error) {
	if err := validateUsername(in.Username); err != nil {
		return Result{}, err
	}
	limit := in.FailureLimit
	if limit <= 0 {
		limit = defaultFailureLimit
	}
	if in.Failures >= limit {
		return Result{Action: ActionLocked, Reason: "too many failed password checks", Failures: in.Failures}, nil
	}
	if !in.UserExists {
		return Result{Action: ActionCreate, Reason: "linux user does not exist", Failures: in.Failures}, nil
	}
	if in.PasswordOK {
		return Result{Action: ActionReuse, Reason: "existing user and password matched", Failures: in.Failures}, nil
	}
	return Result{Action: ActionRename, Reason: "username taken and password did not match", Failures: in.Failures}, nil
}

// SafeError strips values that must never appear in logs. Callers pass the
// secret strings they handled in this request, including model-key values.
// Replacement is a single stdlib pass. A secret of "a" or "redacted" must
// not be scanned again inside the replacement text.
func redactText(msg string, secrets ...string) string {
	secrets = append([]string(nil), secrets...)
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := []string{}
	for _, secret := range secrets {
		if secret != "" {
			pairs = append(pairs, secret, "[redacted]")
		}
	}
	if len(pairs) == 0 {
		return msg
	}
	return strings.NewReplacer(pairs...).Replace(msg)
}

func SafeError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	if classified, ok := err.(ClassifiedError); ok {
		return classified
	}
	return fmt.Errorf("%s", redactText(err.Error(), secrets...))
}
