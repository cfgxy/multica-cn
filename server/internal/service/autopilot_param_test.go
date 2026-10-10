package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-641 stage A: parameter definition validation, trigger-time
// resolution, snapshot merging and template interpolation.

func def(name, typ string, mutate func(*db.AutopilotParam)) db.AutopilotParam {
	d := db.AutopilotParam{
		ID:          pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Name:        name,
		ParamType:   typ,
		Description: "",
		EnumOptions: []string{},
	}
	if mutate != nil {
		mutate(&d)
	}
	return d
}

func required(d db.AutopilotParam) db.AutopilotParam {
	d.Required = true
	return d
}

func withDefault(d db.AutopilotParam, v string) db.AutopilotParam {
	d.DefaultValue = pgtype.Text{String: v, Valid: true}
	return d
}

func rawParams(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	if s == "" {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal raw params: %v", err)
	}
	return m
}

func TestValidateAutopilotParamInputs(t *testing.T) {
	valid := []AutopilotParamInput{
		{Name: "env", ParamType: "string", Required: true},
		{Name: "notes", ParamType: "text", DefaultValue: strPtr("hello")},
		{Name: "port", ParamType: "number", DefaultValue: strPtr("8080")},
		{Name: "verbose", ParamType: "boolean"},
		{Name: "level", ParamType: "enum", EnumOptions: []string{"debug", "info"}, DefaultValue: strPtr("info")},
	}
	if err := ValidateAutopilotParamInputs(valid); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}

	cases := []struct {
		name   string
		inputs []AutopilotParamInput
	}{
		{"bad name chars", []AutopilotParamInput{{Name: "my-param", ParamType: "string"}}},
		{"name starting with digit", []AutopilotParamInput{{Name: "1env", ParamType: "string"}}},
		{"empty name", []AutopilotParamInput{{Name: "", ParamType: "string"}}},
		{"reserved date", []AutopilotParamInput{{Name: "date", ParamType: "string"}}},
		{"duplicate names", []AutopilotParamInput{
			{Name: "env", ParamType: "string"},
			{Name: "env", ParamType: "string"},
		}},
		{"unknown type", []AutopilotParamInput{{Name: "x", ParamType: "float"}}},
		{"enum without options", []AutopilotParamInput{{Name: "level", ParamType: "enum"}}},
		{"enum default outside options", []AutopilotParamInput{
			{Name: "level", ParamType: "enum", EnumOptions: []string{"debug"}, DefaultValue: strPtr("info")},
		}},
		{"duplicate enum options", []AutopilotParamInput{
			{Name: "level", ParamType: "enum", EnumOptions: []string{"debug", "debug"}},
		}},
		{"number default not numeric", []AutopilotParamInput{
			{Name: "port", ParamType: "number", DefaultValue: strPtr("eighty")},
		}},
		{"boolean default not boolean", []AutopilotParamInput{
			{Name: "verbose", ParamType: "boolean", DefaultValue: strPtr("yes")},
		}},
		{"enum options on string", []AutopilotParamInput{
			{Name: "env", ParamType: "string", EnumOptions: []string{"a"}},
		}},
		{"description too long", []AutopilotParamInput{
			{Name: "env", ParamType: "string", Description: strings.Repeat("x", 501)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAutopilotParamInputs(tc.inputs)
			if err == nil {
				t.Fatalf("expected rejection")
			}
			var paramErr *AutopilotParamError
			if !asAutopilotParamError(err, &paramErr) {
				t.Fatalf("error %T is not *AutopilotParamError: %v", err, err)
			}
		})
	}

	tooMany := make([]AutopilotParamInput, 51)
	for i := range tooMany {
		tooMany[i] = AutopilotParamInput{Name: "p" + strings.Repeat("a", i), ParamType: "string"}
	}
	if err := ValidateAutopilotParamInputs(tooMany); err == nil {
		t.Fatalf("expected over-cap set rejection")
	}
}

func strPtr(s string) *string { return &s }

func asAutopilotParamError(err error, target **AutopilotParamError) bool {
	e, ok := err.(*AutopilotParamError)
	if ok {
		*target = e
	}
	return ok
}

func TestResolveAutopilotParamValues(t *testing.T) {
	t.Run("no defs and no raw resolves to nil", func(t *testing.T) {
		values, err := ResolveAutopilotParamValues(nil, nil)
		if err != nil || values != nil {
			t.Fatalf("values=%v err=%v, want nil/nil", values, err)
		}
	})

	t.Run("no defs rejects any supplied param", func(t *testing.T) {
		_, err := ResolveAutopilotParamValues(nil, rawParams(t, `{"env":"prod"}`))
		var paramErr *AutopilotParamError
		if !asAutopilotParamError(err, &paramErr) {
			t.Fatalf("want AutopilotParamError, got %v", err)
		}
		if !strings.Contains(paramErr.Reason, "env") {
			t.Fatalf("reason %q does not name the parameter", paramErr.Reason)
		}
	})

	t.Run("defaults fill, types coerce", func(t *testing.T) {
		defs := []db.AutopilotParam{
			withDefault(def("env", "string", nil), "prod"),
			withDefault(def("port", "number", nil), "8080"),
			withDefault(def("verbose", "boolean", nil), "true"),
			def("notes", "text", nil),
		}
		values, err := ResolveAutopilotParamValues(defs, nil)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if values["env"] != "prod" {
			t.Fatalf("env = %v", values["env"])
		}
		if values["port"] != float64(8080) {
			t.Fatalf("port = %#v, want float64(8080)", values["port"])
		}
		if values["verbose"] != true {
			t.Fatalf("verbose = %#v", values["verbose"])
		}
		if _, ok := values["notes"]; ok {
			t.Fatalf("notes has no default and is optional; must stay absent, got %#v", values["notes"])
		}
	})

	t.Run("supplied values override defaults", func(t *testing.T) {
		defs := []db.AutopilotParam{
			withDefault(def("env", "string", nil), "prod"),
			withDefault(def("port", "number", nil), "8080"),
		}
		values, err := ResolveAutopilotParamValues(defs, rawParams(t, `{"env":"staging","port":9090}`))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if values["env"] != "staging" || values["port"] != float64(9090) {
			t.Fatalf("values = %#v", values)
		}
	})

	t.Run("missing required without default rejects", func(t *testing.T) {
		defs := []db.AutopilotParam{required(def("env", "string", nil))}
		_, err := ResolveAutopilotParamValues(defs, nil)
		var paramErr *AutopilotParamError
		if !asAutopilotParamError(err, &paramErr) {
			t.Fatalf("want AutopilotParamError, got %v", err)
		}
		if !strings.Contains(paramErr.Reason, "missing required parameter") || !strings.Contains(paramErr.Reason, "env") {
			t.Fatalf("reason %q unclear", paramErr.Reason)
		}
	})

	t.Run("missing required with default resolves default", func(t *testing.T) {
		defs := []db.AutopilotParam{required(withDefault(def("env", "string", nil), "prod"))}
		values, err := ResolveAutopilotParamValues(defs, nil)
		if err != nil || values["env"] != "prod" {
			t.Fatalf("values=%v err=%v", values, err)
		}
	})

	t.Run("type mismatches reject without echoing values", func(t *testing.T) {
		defs := []db.AutopilotParam{
			def("env", "string", nil),
			def("port", "number", nil),
			def("verbose", "boolean", nil),
			def("level", "enum", func(d *db.AutopilotParam) { d.EnumOptions = []string{"debug", "info"} }),
		}
		for name, body := range map[string]string{
			"string gets number":  `{"env":42}`,
			"number gets string":  `{"port":"8080"}`,
			"boolean gets string": `{"verbose":"true"}`,
			"enum outside options": `{"level":"trace"}`,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := ResolveAutopilotParamValues(defs, rawParams(t, body))
				var paramErr *AutopilotParamError
				if !asAutopilotParamError(err, &paramErr) {
					t.Fatalf("want AutopilotParamError, got %v", err)
				}
				if strings.Contains(paramErr.Reason, "8080") || strings.Contains(paramErr.Reason, "trace") || strings.Contains(paramErr.Reason, "true\"") {
					t.Fatalf("reason %q echoes caller values", paramErr.Reason)
				}
			})
		}
	})

	t.Run("unknown supplied name rejects", func(t *testing.T) {
		defs := []db.AutopilotParam{def("env", "string", nil)}
		_, err := ResolveAutopilotParamValues(defs, rawParams(t, `{"env":"prod","h ost":"x"}`))
		// "h ost" is not a valid param name client-side, but the resolver only
		// knows it is not defined here — it must reject by name.
		if err == nil {
			t.Fatalf("expected unknown-parameter rejection")
		}
	})

	t.Run("non-object params rejected by extractor", func(t *testing.T) {
		payload := []byte(`{"params":"oops"}`)
		_, err := extractRawAutopilotParams(payload)
		var paramErr *AutopilotParamError
		if !asAutopilotParamError(err, &paramErr) {
			t.Fatalf("want AutopilotParamError, got %v", err)
		}
	})

	t.Run("payload without params yields nothing", func(t *testing.T) {
		raw, err := extractRawAutopilotParams([]byte(`{"event":"push"}`))
		if err != nil || raw != nil {
			t.Fatalf("raw=%v err=%v", raw, err)
		}
		raw, err = extractRawAutopilotParams(nil)
		if err != nil || raw != nil {
			t.Fatalf("raw=%v err=%v", raw, err)
		}
	})
}

func TestMergeAutopilotParamSnapshot(t *testing.T) {
	t.Run("no values returns payload unchanged", func(t *testing.T) {
		payload := []byte(`{"event":"push"}`)
		merged, err := mergeAutopilotParamSnapshot(payload, nil)
		if err != nil || string(merged) != string(payload) {
			t.Fatalf("merged=%s err=%v", merged, err)
		}
	})

	t.Run("nil payload becomes params-only object", func(t *testing.T) {
		merged, err := mergeAutopilotParamSnapshot(nil, map[string]any{"env": "prod"})
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		var obj map[string]any
		if err := json.Unmarshal(merged, &obj); err != nil {
			t.Fatalf("merged payload not an object: %v (%s)", err, merged)
		}
		if obj["params"].(map[string]any)["env"] != "prod" {
			t.Fatalf("params missing: %s", merged)
		}
	})

	t.Run("object payload keeps siblings and overwrites raw params", func(t *testing.T) {
		payload := []byte(`{"event":"push","params":{"env":"RAW"}}`)
		merged, err := mergeAutopilotParamSnapshot(payload, map[string]any{"env": "prod", "port": float64(8080)})
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		var obj map[string]any
		if err := json.Unmarshal(merged, &obj); err != nil {
			t.Fatalf("merged payload not an object: %v", err)
		}
		if obj["event"] != "push" {
			t.Fatalf("sibling event lost: %s", merged)
		}
		params := obj["params"].(map[string]any)
		if params["env"] != "prod" || params["port"] != float64(8080) {
			t.Fatalf("resolved params wrong: %#v", params)
		}
	})

	t.Run("non-object payload preserved under payload key", func(t *testing.T) {
		payload := []byte(`[1,2,3]`)
		merged, err := mergeAutopilotParamSnapshot(payload, map[string]any{"env": "prod"})
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		var obj map[string]any
		if err := json.Unmarshal(merged, &obj); err != nil {
			t.Fatalf("merged payload not an object: %v (%s)", err, merged)
		}
		if obj["payload"] != "[1,2,3]" {
			t.Fatalf("original payload lost: %s", merged)
		}
	})

	t.Run("round trip through run snapshot reader", func(t *testing.T) {
		merged, err := mergeAutopilotParamSnapshot([]byte(`{"event":"push"}`), map[string]any{"env": "prod", "port": float64(8080), "verbose": true})
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		run := db.AutopilotRun{TriggerPayload: merged}
		values := autopilotParamValuesFromRun(run)
		if values["env"] != "prod" || values["port"] != float64(8080) || values["verbose"] != true {
			t.Fatalf("values = %#v", values)
		}
		if values := autopilotParamValuesFromRun(db.AutopilotRun{}); values != nil {
			t.Fatalf("empty payload should read as nil, got %#v", values)
		}
	})
}

func TestRenderAutopilotParamValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"prod", "prod"},
		{true, "true"},
		{float64(8080), "8080"},
		{float64(1.5), "1.5"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := renderAutopilotParamValue(tc.in); got != tc.want {
			t.Fatalf("render(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateTemplateTokens(t *testing.T) {
	names := map[string]bool{"env": true}

	if err := ValidateTemplateTokens("", names); err != nil {
		t.Fatalf("empty text must pass: %v", err)
	}
	if err := ValidateTemplateTokens("Deploy to {{ env }} at {{date}}", names); err != nil {
		t.Fatalf("built-ins and defined params must pass: %v", err)
	}
	err := ValidateTemplateTokens("Deploy {{environment}}", names)
	var paramErr *AutopilotParamError
	if !asAutopilotParamError(err, &paramErr) {
		t.Fatalf("unknown token must be an AutopilotParamError, got %v", err)
	}
	if !strings.Contains(paramErr.Reason, "environment") {
		t.Fatalf("error must name the offending token: %q", paramErr.Reason)
	}

	// The legacy entry point (no params) keeps rejecting non-built-ins.
	if err := ValidateIssueTitleTemplate("Deploy {{env}}"); err == nil {
		t.Fatalf("legacy whitelist must still reject undefined tokens")
	}
	if err := ValidateIssueTitleTemplate("Deploy {{date}}"); err != nil {
		t.Fatalf("legacy whitelist must keep {{date}}: %v", err)
	}
}

func TestInterpolateAutopilotTextWithParams(t *testing.T) {
	svc := &AutopilotService{}
	payload, err := mergeAutopilotParamSnapshot(nil, map[string]any{
		"env":     "prod",
		"port":    float64(8080),
		"verbose": true,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	run := db.AutopilotRun{TriggerPayload: payload}

	got := svc.interpolateAutopilotText("Deploy {{env}}:{{port}} verbose={{verbose}} on {{date}}", run, "UTC")
	if !strings.Contains(got, "Deploy prod:8080 verbose=true") {
		t.Fatalf("interpolation wrong: %q", got)
	}
	if strings.Contains(got, "{{date}}") {
		t.Fatalf("{{date}} must still interpolate: %q", got)
	}

	// Unknown tokens pass through literally — the render layer never invents
	// values for names validation would have rejected.
	got = svc.interpolateAutopilotText("keep {{undefined_token}} as-is", run, "UTC")
	if got != "keep {{undefined_token}} as-is" {
		t.Fatalf("unknown token must pass through: %q", got)
	}

	// A run without a snapshot only knows {{date}}.
	plain := db.AutopilotRun{}
	got = svc.interpolateAutopilotText("{{env}} on {{date}}", plain, "UTC")
	if !strings.Contains(got, "{{env}}") || strings.Contains(got, "{{date}}") {
		t.Fatalf("snapshot-less run must keep param tokens and keep date: %q", got)
	}
}
