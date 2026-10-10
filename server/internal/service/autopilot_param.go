package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Autopilot trigger parameters (RUYI-641 stage A). A parameter definition is
// one entry in an autopilot's calling contract: triggers may pass a `params`
// object (webhook body, manual /trigger request), schedule fires resolve
// defaults, and the resolved values substitute {{name}} tokens in the
// issue-title template and task instructions before being snapshotted into
// autopilot_run.trigger_payload.

// Autopilot parameter types.
const (
	AutopilotParamTypeString  = "string"
	AutopilotParamTypeText    = "text"
	AutopilotParamTypeNumber  = "number"
	AutopilotParamTypeBoolean = "boolean"
	AutopilotParamTypeEnum    = "enum"
)

const (
	maxAutopilotParams        = 50
	maxAutopilotParamDescLen  = 500
	maxAutopilotEnumOptions   = 50
	maxAutopilotEnumOptionLen = 200
	// Render-time value caps keep a resolved parameter from blowing up the
	// title template or the issue description. text allows more than string
	// by design; both dwarf realistic template use.
	maxAutopilotStringValueLen  = 1000
	maxAutopilotTextValueLen    = 20000
	maxAutopilotEnumValueLen    = maxAutopilotEnumOptionLen
)

// autopilotParamNameRE constrains the name to identifier syntax: the name
// doubles as a {{name}} template token, so anything the token regex could
// capture but a template author cannot write unambiguously is rejected.
var autopilotParamNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// AutopilotParamInput is one parameter definition as submitted by a client.
type AutopilotParamInput struct {
	Name         string   `json:"name"`
	ParamType    string   `json:"param_type"`
	Description  string   `json:"description"`
	Required     bool     `json:"required"`
	DefaultValue *string  `json:"default_value"`
	EnumOptions  []string `json:"enum_options"`
}

// AutopilotParamError marks a caller-fixable parameter problem: an invalid
// definition at save time, or invalid trigger-time values. The manual
// /trigger surface maps it to a 400; automation sources record it as the
// run's skip reason. Reasons name parameters and expectations but never echo
// caller values back — parameter values are business data.
type AutopilotParamError struct {
	Reason string
}

func (e *AutopilotParamError) Error() string { return e.Reason }

func paramErrf(format string, args ...any) error {
	return &AutopilotParamError{Reason: fmt.Sprintf(format, args...)}
}

// ValidateAutopilotParamInputs validates a submitted definition set as a
// whole: per-parameter shape plus set-level uniqueness. Called at save time
// (create/update/PUT params) so a rejected set never reaches the database.
func ValidateAutopilotParamInputs(inputs []AutopilotParamInput) error {
	if len(inputs) > maxAutopilotParams {
		return paramErrf("too many parameters: %d (max %d)", len(inputs), maxAutopilotParams)
	}
	seen := make(map[string]bool, len(inputs))
	for i, in := range inputs {
		label := in.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		if !autopilotParamNameRE.MatchString(in.Name) {
			return paramErrf("parameter %q: name must match [A-Za-z_][A-Za-z0-9_]* (max 50 chars)", label)
		}
		if in.Name == "date" {
			return paramErrf("parameter %q: this name is reserved for the built-in {{date}} token", in.Name)
		}
		if seen[in.Name] {
			return paramErrf("duplicate parameter name %q", in.Name)
		}
		seen[in.Name] = true
		if len(in.Description) > maxAutopilotParamDescLen {
			return paramErrf("parameter %q: description too long (max %d chars)", in.Name, maxAutopilotParamDescLen)
		}
		switch in.ParamType {
		case AutopilotParamTypeString, AutopilotParamTypeText:
			if len(in.EnumOptions) > 0 {
				return paramErrf("parameter %q: enum_options only applies to enum parameters", in.Name)
			}
		case AutopilotParamTypeNumber:
			if len(in.EnumOptions) > 0 {
				return paramErrf("parameter %q: enum_options only applies to enum parameters", in.Name)
			}
			if in.DefaultValue != nil {
				if _, err := strconv.ParseFloat(strings.TrimSpace(*in.DefaultValue), 64); err != nil {
					return paramErrf("parameter %q: default_value must be a number", in.Name)
				}
			}
		case AutopilotParamTypeBoolean:
			if len(in.EnumOptions) > 0 {
				return paramErrf("parameter %q: enum_options only applies to enum parameters", in.Name)
			}
			if in.DefaultValue != nil {
				switch strings.ToLower(strings.TrimSpace(*in.DefaultValue)) {
				case "true", "false":
				default:
					return paramErrf("parameter %q: default_value must be true or false", in.Name)
				}
			}
		case AutopilotParamTypeEnum:
			if len(in.EnumOptions) == 0 {
				return paramErrf("parameter %q: enum parameters require at least one option", in.Name)
			}
			if len(in.EnumOptions) > maxAutopilotEnumOptions {
				return paramErrf("parameter %q: too many enum options (max %d)", in.Name, maxAutopilotEnumOptions)
			}
			optSeen := make(map[string]bool, len(in.EnumOptions))
			for _, opt := range in.EnumOptions {
				if opt == "" || len(opt) > maxAutopilotEnumOptionLen {
					return paramErrf("parameter %q: enum options must be 1-%d chars", in.Name, maxAutopilotEnumOptionLen)
				}
				if optSeen[opt] {
					return paramErrf("parameter %q: duplicate enum option", in.Name)
				}
				optSeen[opt] = true
			}
			if in.DefaultValue != nil && !optSeen[*in.DefaultValue] {
				return paramErrf("parameter %q: default_value must be one of the enum options", in.Name)
			}
		default:
			return paramErrf("parameter %q: unknown param_type %q (supported: string, text, number, boolean, enum)", in.Name, in.ParamType)
		}
	}
	return nil
}

// ResolveAutopilotParamValues validates raw trigger-time values against the
// definitions and returns the resolved typed values with defaults applied.
// nil raw (schedule fires) resolves from defaults alone; an unknown supplied
// name, a missing required parameter without a default, or a type mismatch
// returns an *AutopilotParamError.
func ResolveAutopilotParamValues(defs []db.AutopilotParam, raw map[string]json.RawMessage) (map[string]any, error) {
	if len(defs) == 0 {
		// No contract defined: any supplied params are a caller mistake —
		// silently dropping them would make a typo look like success.
		for name := range raw {
			return nil, paramErrf("unknown parameter %q: this autopilot defines no parameters", name)
		}
		return nil, nil
	}
	values := make(map[string]any, len(defs))
	supplied := make(map[string]bool, len(raw))
	for name := range raw {
		supplied[name] = true
	}
	for _, def := range defs {
		if rawVal, ok := raw[def.Name]; ok {
			supplied[def.Name] = false
			v, err := coerceAutopilotParamJSONValue(def, rawVal)
			if err != nil {
				return nil, err
			}
			if v != nil {
				values[def.Name] = v
			}
			continue
		}
		supplied[def.Name] = false
		if def.DefaultValue.Valid {
			v, err := coerceAutopilotParamDefault(def, def.DefaultValue.String)
			if err != nil {
				return nil, err
			}
			values[def.Name] = v
			continue
		}
		if def.Required {
			return nil, paramErrf("missing required parameter %q", def.Name)
		}
	}
	for name, wasSupplied := range supplied {
		if wasSupplied {
			return nil, paramErrf("unknown parameter %q", name)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

// coerceAutopilotParamJSONValue converts one raw JSON value to the
// definition's type. Coercion is strict: no string→number, number→string or
// "true"-string→boolean acceptance, because a caller that sends the wrong
// JSON type almost certainly means something different by it.
func coerceAutopilotParamJSONValue(def db.AutopilotParam, rawVal json.RawMessage) (any, error) {
	switch def.ParamType {
	case AutopilotParamTypeString, AutopilotParamTypeText:
		var s string
		if err := json.Unmarshal(rawVal, &s); err != nil {
			return nil, paramErrf("parameter %q: expected a string value", def.Name)
		}
		limit := maxAutopilotStringValueLen
		if def.ParamType == AutopilotParamTypeText {
			limit = maxAutopilotTextValueLen
		}
		if len(s) > limit {
			return nil, paramErrf("parameter %q: value too long (max %d chars)", def.Name, limit)
		}
		return s, nil
	case AutopilotParamTypeNumber:
		var f float64
		if err := json.Unmarshal(rawVal, &f); err != nil {
			return nil, paramErrf("parameter %q: expected a number value", def.Name)
		}
		return f, nil
	case AutopilotParamTypeBoolean:
		var b bool
		if err := json.Unmarshal(rawVal, &b); err != nil {
			return nil, paramErrf("parameter %q: expected a boolean value", def.Name)
		}
		return b, nil
	case AutopilotParamTypeEnum:
		var s string
		if err := json.Unmarshal(rawVal, &s); err != nil {
			return nil, paramErrf("parameter %q: expected a string value", def.Name)
		}
		if len(s) > maxAutopilotEnumValueLen || !slices.Contains(def.EnumOptions, s) {
			return nil, paramErrf("parameter %q: value is not one of the allowed options", def.Name)
		}
		return s, nil
	default:
		return nil, paramErrf("parameter %q: unknown param_type %q", def.Name, def.ParamType)
	}
}

// coerceAutopilotParamDefault converts a definition's stored default_value
// text to the resolved type. Defaults were validated at save time; the
// re-check keeps a stale row from poisoning a dispatch.
func coerceAutopilotParamDefault(def db.AutopilotParam, raw string) (any, error) {
	switch def.ParamType {
	case AutopilotParamTypeNumber:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, paramErrf("parameter %q: default_value must be a number", def.Name)
		}
		return f, nil
	case AutopilotParamTypeBoolean:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, paramErrf("parameter %q: default_value must be true or false", def.Name)
		}
	default:
		return raw, nil
	}
}

// extractRawAutopilotParams pulls the `params` object out of a trigger
// payload. A payload that is not a JSON object carries no params (webhook
// envelopes and manual payloads are always objects; this is defense, not a
// supported shape). `params` present but not an object is a caller error.
func extractRawAutopilotParams(payload []byte) (map[string]json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, nil
	}
	rawParams, ok := obj["params"]
	if !ok {
		return nil, nil
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, paramErrf("params must be an object keyed by parameter name")
	}
	return params, nil
}

// mergeAutopilotParamSnapshot merges resolved values into the stored trigger
// payload under the `params` key — the audit snapshot AND the interpolation
// source in one place. An existing `params` key (the raw submitted values)
// is overwritten by the resolved values, so the snapshot records what the
// run actually used. A non-object payload is preserved under `payload`.
func mergeAutopilotParamSnapshot(payload []byte, values map[string]any) ([]byte, error) {
	if len(values) == 0 {
		return payload, nil
	}
	snapshot := make(map[string]any, 4)
	if len(payload) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(payload, &obj); err == nil {
			for k, v := range obj {
				snapshot[k] = v
			}
		} else {
			snapshot["payload"] = string(payload)
		}
	}
	snapshot["params"] = values
	return json.Marshal(snapshot)
}

// autopilotParamValuesFromRun reads the resolved parameter snapshot back out
// of a run's trigger payload. No snapshot or unparsable payload means no
// parameters — templates then fall back to built-ins only.
func autopilotParamValuesFromRun(run db.AutopilotRun) map[string]any {
	if len(run.TriggerPayload) == 0 {
		return nil
	}
	var obj struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(run.TriggerPayload, &obj); err != nil {
		return nil
	}
	return obj.Params
}

// renderAutopilotParamValue formats a resolved value for template
// substitution. JSON numbers arrive as float64; integral values render
// without a decimal point so {{port}} becomes "8080", not "8080.0".
func renderAutopilotParamValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

// templateParamNames extracts the name whitelist a submitted definition set
// contributes to template validation.
func templateParamNames(inputs []AutopilotParamInput) map[string]bool {
	names := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		names[in.Name] = true
	}
	return names
}

// paramNamesHint renders the supported-token suffix for validation errors in
// a deterministic order.
func paramNamesHint(names map[string]bool) string {
	if len(names) == 0 {
		return ""
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	parts := make([]string, 0, len(sorted))
	for _, name := range sorted {
		parts = append(parts, "{{"+name+"}}")
	}
	return ", " + strings.Join(parts, ", ")
}

// resolveAutopilotTriggerParams resolves and snapshots the trigger-time
// parameters for a dispatch. It returns the payload to persist: unchanged
// when the autopilot defines no parameters and the caller supplied none,
// otherwise the payload with `params` set to the resolved values.
func (s *AutopilotService) resolveAutopilotTriggerParams(ctx context.Context, autopilot db.Autopilot, payload []byte) ([]byte, error) {
	raw, err := extractRawAutopilotParams(payload)
	if err != nil {
		return nil, err
	}
	defs, err := s.Queries.ListAutopilotParams(ctx, autopilot.ID)
	if err != nil {
		return nil, fmt.Errorf("list autopilot params: %w", err)
	}
	if len(defs) == 0 && len(raw) == 0 {
		return payload, nil
	}
	values, err := ResolveAutopilotParamValues(defs, raw)
	if err != nil {
		return nil, err
	}
	return mergeAutopilotParamSnapshot(payload, values)
}
