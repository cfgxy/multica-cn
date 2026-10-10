package computer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/sub2api"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
	"github.com/tidwall/sjson"
	"gopkg.in/yaml.v3"
)

type ModelConfigFile struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Format  string `json:"format"`
	Content string `json:"content"`
	// Bindings map structural field paths to a fixed rendering variable.
	Bindings  map[string]string `json:"bindings"`
	Selectors map[string]string `json:"selectors,omitempty"`
}
type ModelTemplate struct {
	RuntimeKind       string            `json:"runtime_kind"`
	Revision          int64             `json:"revision"`
	Files             []ModelConfigFile `json:"files"`
	SupportedAPITypes []string          `json:"supported_api_types"`
	DefaultModel      string            `json:"default_model"`
	Status            string            `json:"status"`
}

var configRoots = map[string]string{
	"claude": ".claude", "codex": ".codex", "opencode": ".config/opencode", "openclaw": ".openclaw",
	"grok": ".grok", "kimi": ".kimi", "omp": ".omp/agent", "pi": ".pi/agent",
}

func ModelConfigPathAllowed(kind, p string) bool {
	root, ok := configRoots[kind]
	return ok && validateRuntimeConfigPath(p) == nil && path.Clean(p) == p && strings.HasPrefix(p, root+"/") &&
		(strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonc") || strings.HasSuffix(p, ".toml") || strings.HasSuffix(p, ".yml"))
}
func (t *ModelTemplate) Validate() error {
	if !sub2api.KnownRuntime(t.RuntimeKind) || len(t.Files) == 0 || len(t.Files) > 16 || len(t.SupportedAPITypes) == 0 {
		return ClassifiedError("model_template_invalid")
	}
	seen := map[string]bool{}
	variables := map[string]bool{}
	for i, f := range t.Files {
		if f.ID == "" || seen[f.ID] || seen[f.Path] || !ModelConfigPathAllowed(t.RuntimeKind, f.Path) || len(f.Content) > 1<<20 {
			return ClassifiedError("model_template_invalid")
		}
		seen[f.ID] = true
		seen[f.Path] = true
		root, err := parseModelFile(f.Format, f.Content)
		if err != nil {
			return err
		}
		t.Files[i].Selectors = modelProviderSelectors(t.RuntimeKind, f, root)
		checks := map[string]string{}
		for key, v := range f.Bindings {
			if key == "" || strings.ContainsAny(key, "*?\\") || !sub2api.Contains([]string{"api_key_token", "base_url", "model_id", "api_type"}, v) {
				return ClassifiedError("model_template_invalid")
			}
			if v == "api_key_token" {
				raw, _ := json.Marshal(root)
				placeholder := gjson.GetBytes(raw, key).String()
				if placeholder != "" && placeholder != "{{api_key_token}}" && placeholder != "${api_key_token}" {
					return ClassifiedError("model_template_invalid")
				}
			}
			checks[key] = "test-value"
			variables[v] = true
		}
		if _, err := patchModelFields(f.Format, f.Content, checks); err != nil {
			return err
		}
	}
	if !variables["api_key_token"] || !variables["base_url"] || !variables["model_id"] {
		return ClassifiedError("model_template_invalid")
	}
	for _, p := range t.SupportedAPITypes {
		if !sub2api.KnownProtocol(p) {
			return ClassifiedError("model_template_invalid")
		}
	}
	return nil
}

func BuiltinModelTemplate(kind string) (ModelTemplate, error) {
	t := ModelTemplate{RuntimeKind: kind, Files: []ModelConfigFile{}, Revision: 0, Status: "builtin", SupportedAPITypes: sub2api.RuntimeProtocols(kind)}
	add := func(id, p, format, content string, bindings map[string]string) {
		t.Files = append(t.Files, ModelConfigFile{ID: id, Path: p, Format: format, Content: content, Bindings: bindings})
	}
	switch kind {
	case "claude":
		add("settings", ".claude/settings.json", "json", `{"env":{"ANTHROPIC_AUTH_TOKEN":"","ANTHROPIC_BASE_URL":""},"model":""}`, map[string]string{"env.ANTHROPIC_AUTH_TOKEN": "api_key_token", "env.ANTHROPIC_BASE_URL": "base_url", "model": "model_id"})
	case "codex":
		add("config", ".codex/config.toml", "toml", "model = \"\"\nmodel_provider = \"multica\"\n[model_providers.multica]\nname = \"Multica\"\nbase_url = \"\"\nwire_api = \"responses\"\nrequires_openai_auth = true\n", map[string]string{"model": "model_id", "model_providers.multica.base_url": "base_url"})
		add("auth", ".codex/auth.json", "json", `{"OPENAI_API_KEY":""}`, map[string]string{"OPENAI_API_KEY": "api_key_token"})
	case "opencode":
		add("config", ".config/opencode/opencode.json", "json", `{"provider":{"multica":{"npm":"@ai-sdk/openai-compatible","name":"Multica","options":{"baseURL":"","apiKey":""},"models":{"multica":{"name":"Multica","id":""}}}},"model":"multica/multica"}`, map[string]string{"provider.multica.options.baseURL": "base_url", "provider.multica.options.apiKey": "api_key_token", "provider.multica.models.multica.id": "model_id"})
	case "grok":
		add("config", ".grok/config.toml", "toml", "[models]\ndefault = \"multica\"\n[model.multica]\nname = \"Multica\"\nmodel = \"\"\nbase_url = \"\"\napi_key = \"\"\napi_backend = \"responses\"\ncontext_window = 128000\n", map[string]string{"model.multica.model": "model_id", "model.multica.base_url": "base_url", "model.multica.api_key": "api_key_token"})
	case "kimi":
		add("config", ".kimi/config.toml", "toml", "default_model = \"multica\"\n[providers.multica]\ntype = \"openai_legacy\"\nbase_url = \"\"\napi_key = \"\"\n[models.multica]\nprovider = \"multica\"\nmodel = \"\"\nmax_context_size = 128000\n", map[string]string{"providers.multica.base_url": "base_url", "providers.multica.api_key": "api_key_token", "models.multica.model": "model_id"})
	case "pi", "omp":
		root := configRoots[kind]
		format := "json"
		p := root + "/models.json"
		if kind == "omp" {
			format = "yaml"
			p = root + "/models.yml"
		}
		// JSON is also valid YAML; the YAML writer preserves comments on subsequent edits.
		add("models", p, format, `{"providers":{"multica":{"baseUrl":"","apiKey":"","api":"openai-completions","models":[{"id":"","name":"Multica","contextWindow":128000,"maxTokens":4096,"reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, map[string]string{"providers.multica.baseUrl": "base_url", "providers.multica.apiKey": "api_key_token", "providers.multica.api": "api_type", "providers.multica.models.0.id": "model_id"})
		if kind == "pi" {
			add("settings", root+"/settings.json", "json", `{"defaultProvider":"multica","defaultModel":""}`, map[string]string{"defaultModel": "model_id"})
		} else {
			add("settings", root+"/config.yml", "yaml", "modelRoles:\n  default: ''\n", map[string]string{"modelRoles.default": "model_id"})
		}
	case "openclaw":
		add("config", ".openclaw/openclaw.json", "json", `{"models":{"mode":"merge","providers":{"multica":{"baseUrl":"","apiKey":"","api":"openai-completions","models":[{"id":"","name":"Multica","contextWindow":128000,"maxTokens":4096,"reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}},"agents":{"defaults":{"model":{"primary":"multica/"}}}}`, map[string]string{"models.providers.multica.baseUrl": "base_url", "models.providers.multica.apiKey": "api_key_token", "models.providers.multica.api": "api_type", "models.providers.multica.models.0.id": "model_id", "agents.defaults.model.primary": "model_id"})
	default:
		return t, ClassifiedError("model_template_invalid")
	}
	return t, t.Validate()
}

func parseModelFile(format, content string) (map[string]any, error) {
	var root map[string]any
	var err error
	switch format {
	case "jsonc":
		err = json.Unmarshal(pretty.Spec([]byte(content)), &root)
	case "json":
		err = json.Unmarshal([]byte(content), &root)
	case "toml":
		err = toml.Unmarshal([]byte(content), &root)
	case "yaml":
		err = yaml.Unmarshal([]byte(content), &root)
	default:
		err = errors.New("unsupported format")
	}
	if err != nil || root == nil {
		return nil, ClassifiedError("model_config_invalid")
	}
	return root, nil
}
func ValidateModelFiles(t ModelTemplate, files map[string]string) error {
	if len(files) != len(t.Files) {
		return ClassifiedError("model_config_incomplete")
	}
	return ValidateModelFileEdits(t, files)
}

// Manual repairs may update an existing subset without inventing missing secrets.
func ValidateModelFileEdits(t ModelTemplate, files map[string]string) error {
	allowed := map[string]ModelConfigFile{}
	for _, f := range t.Files {
		allowed[f.Path] = f
	}
	if len(files) == 0 || len(files) > len(allowed) {
		return ClassifiedError("model_config_invalid")
	}
	for p, c := range files {
		f, ok := allowed[p]
		if !ok || len(c) > 1<<20 || strings.ContainsRune(c, 0) {
			return ClassifiedError("model_config_invalid")
		}
		if _, err := parseModelFile(f.Format, c); err != nil {
			return err
		}
	}
	return nil
}
func ModelSelection(t ModelTemplate, k sub2api.Resolved, modelID, protocol string) (sub2api.Endpoint, string, error) {
	for _, e := range k.Endpoints {
		if (protocol != "" && e.APIType != protocol) || !sub2api.Contains(t.SupportedAPITypes, e.APIType) {
			continue
		}
		model := modelID
		if model == "" {
			model = t.DefaultModel
		}
		if model == "" {
			model = e.DefaultModel
		}
		for _, m := range k.Models {
			if m.ID == model && sub2api.Contains(m.APITypes, e.APIType) {
				return e, model, nil
			}
		}
	}
	return sub2api.Endpoint{}, "", ClassifiedError("model_selection_required")
}

// RenderModelFiles creates new files from a template, or patches only the
// declared fields in existing content. It never reapplies a newer template body.
func RenderModelFiles(t ModelTemplate, k sub2api.Resolved, model, protocol string, existing map[string]string) (map[string]string, string, string, error) {
	e, model, err := ModelSelection(t, k, model, protocol)
	if err != nil {
		return nil, "", "", err
	}
	out := map[string]string{}
	for _, f := range t.Files {
		content := f.Content
		if existing != nil {
			var ok bool
			if existingContent, found := existing[f.Path]; found {
				content = existingContent
				ok = true
			}
			if !ok && content == "" {
				return nil, "", "", ClassifiedError("model_config_incomplete")
			}
		}
		// Selecting a key also selects the provider described by the original
		// template. Unrelated user configuration stays outside this field set.
		values := map[string]string{}
		for field, value := range f.Selectors {
			values[field] = value
		}
		if t.RuntimeKind == "codex" && f.Path == ".codex/auth.json" {
			for _, field := range []string{"auth_mode", "tokens", "last_refresh"} {
				content, _ = sjson.Delete(content, field)
			}
		}
		if t.RuntimeKind == "claude" && f.Format == "json" {
			if f.Bindings["env.ANTHROPIC_AUTH_TOKEN"] == "api_key_token" {
				content, _ = sjson.Delete(content, "env.ANTHROPIC_API_KEY")
			}
			if f.Bindings["env.ANTHROPIC_API_KEY"] == "api_key_token" {
				content, _ = sjson.Delete(content, "env.ANTHROPIC_AUTH_TOKEN")
			}
		}
		for field, v := range f.Bindings {
			switch v {
			case "api_key_token":
				values[field] = k.APIKeyToken
			case "base_url":
				values[field] = e.BaseURL
			case "model_id":
				values[field] = model
				if (t.RuntimeKind == "openclaw" && field == "agents.defaults.model.primary") || (t.RuntimeKind == "omp" && field == "modelRoles.default") {
					values[field] = "multica/" + model
				}
			case "api_type":
				values[field] = e.APIType
				if e.APIType == "openai-chat-completions" {
					values[field] = "openai-completions"
				}
			}
		}
		content, err = patchModelFields(f.Format, content, values)
		if err != nil {
			return nil, "", "", err
		}
		if t.RuntimeKind == "codex" && f.Path == ".codex/config.toml" {
			root, _ := parseModelFile(f.Format, content)
			raw, _ := json.Marshal(root)
			provider := gjson.GetBytes(raw, "model_provider").String()
			fields := gjson.GetBytes(raw, "model_providers").Map()[provider]
			if !fields.Get("requires_openai_auth").Bool() || fields.Get("env_key").String() != "" || fields.Get("experimental_bearer_token").String() != "" {
				return nil, "", "", ClassifiedError("model_config_mapping_missing")
			}
		}
		out[f.Path] = content
	}
	return out, model, e.APIType, ValidateModelFiles(t, out)
}
func patchModelFields(format, content string, values map[string]string) (string, error) {
	if _, err := parseModelFile(format, content); err != nil {
		return "", err
	}
	switch format {
	case "json", "jsonc":
		for k, v := range values {
			// Existing schemas that do not have a managed scalar field need explicit editing.
			field := gjson.Get(content, k)
			if !field.Exists() || (field.Type != gjson.String && field.Type != gjson.Null) {
				return "", ClassifiedError("model_config_mapping_missing")
			}
			next, err := sjson.Set(content, k, v)
			if err != nil {
				return "", ClassifiedError("model_config_invalid")
			}
			content = next
		}
	case "toml":
		type edit struct {
			start, end int
			value      string
		}
		edits := []edit{}
		found := map[string]bool{}
		var p unstable.Parser
		p.Reset([]byte(content))
		table := []string{}
		for p.NextExpression() {
			n := p.Expression()
			keys := []string{}
			it := n.Key()
			for it.Next() {
				keys = append(keys, string(it.Node().Data))
			}
			switch n.Kind {
			case unstable.Table:
				table = keys
			case unstable.ArrayTable:
				table = []string{"__array_table__"}
			case unstable.KeyValue:
				name := strings.Join(append(append([]string{}, table...), keys...), ".")
				v, ok := values[name]
				if !ok {
					continue
				}
				value := n.Value()
				if value.Kind != unstable.String {
					return "", ClassifiedError("model_config_mapping_missing")
				}
				// strconv.Quote may produce Go-only escapes; TOML's encoder provides the correct literal.
				encoded, _ := toml.Marshal(map[string]string{"v": v})
				_, literal, _ := strings.Cut(string(encoded), "=")
				edits = append(edits, edit{int(value.Raw.Offset), int(value.Raw.Offset + value.Raw.Length), strings.TrimSpace(literal)})
				found[name] = true
			}
		}
		if p.Error() != nil || len(found) != len(values) {
			return "", ClassifiedError("model_config_mapping_missing")
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
		for _, e := range edits {
			content = content[:e.start] + e.value + content[e.end:]
		}
	case "yaml":
		var doc yaml.Node
		if yaml.Unmarshal([]byte(content), &doc) != nil {
			return "", ClassifiedError("model_config_invalid")
		}
		for field, v := range values {
			n := doc.Content[0]
			for _, part := range strings.Split(field, ".") {
				var next *yaml.Node
				if n.Kind == yaml.MappingNode {
					for i := 0; i < len(n.Content); i += 2 {
						if n.Content[i].Value == part {
							next = n.Content[i+1]
							break
						}
					}
				}
				if n.Kind == yaml.SequenceNode {
					if i, err := strconv.Atoi(part); err == nil && i >= 0 && i < len(n.Content) {
						next = n.Content[i]
					}
				}
				if next == nil {
					return "", ClassifiedError("model_config_mapping_missing")
				}
				n = next
			}
			if n.Kind != yaml.ScalarNode || (n.Tag != "!!str" && n.Tag != "") {
				return "", ClassifiedError("model_config_mapping_missing")
			}
			n.Kind = yaml.ScalarNode
			n.Tag = "!!str"
			n.Value = v
		}
		b, err := yaml.Marshal(&doc)
		if err != nil {
			return "", ClassifiedError("model_config_invalid")
		}
		content = string(b)
	default:
		return "", fmt.Errorf("unsupported configuration format")
	}
	return content, nil
}

// ConfiguredModel reports the model actually present in the saved files.
func ConfiguredModel(t ModelTemplate, files map[string]string) string {
	model := ""
	for _, f := range t.Files {
		root, err := parseModelFile(f.Format, files[f.Path])
		if err != nil {
			return ""
		}
		raw, _ := json.Marshal(root)
		for field, variable := range f.Bindings {
			if variable != "model_id" {
				continue
			}
			value := gjson.GetBytes(raw, field).String()
			if (t.RuntimeKind == "openclaw" && field == "agents.defaults.model.primary") || (t.RuntimeKind == "omp" && field == "modelRoles.default") {
				value = strings.TrimPrefix(value, "multica/")
			}
			if value == "" || (model != "" && model != value) {
				return ""
			}
			model = value
		}
	}
	return model
}

// ConfiguredModelSelector preserves native aliases/provider prefixes for CLI
// arguments and ACP session/set_model. It is distinct from the upstream model ID.
func ConfiguredModelSelector(t ModelTemplate, files map[string]string) string {
	model := ConfiguredModel(t, files)
	if model == "" {
		return ""
	}
	field := map[string]string{"opencode": "model", "kimi": "default_model", "grok": "models.default", "omp": "modelRoles.default", "openclaw": "agents.defaults.model.primary", "pi": "defaultModel"}[t.RuntimeKind]
	if field == "" {
		return model
	}
	for _, f := range t.Files {
		root, err := parseModelFile(f.Format, files[f.Path])
		if err != nil {
			return ""
		}
		raw, _ := json.Marshal(root)
		if value := gjson.GetBytes(raw, field).String(); value != "" {
			if t.RuntimeKind == "pi" {
				provider := gjson.GetBytes(raw, "defaultProvider").String()
				if provider == "" {
					return ""
				}
				return provider + "/" + value
			}
			return value
		}
	}
	return ""
}

// CheckModelFiles performs semantic checks only for the explicitly managed
// fields. Custom native configurations can still be saved and tested directly.
func CheckModelFiles(t ModelTemplate, files map[string]string) error {
	if err := ValidateModelFiles(t, files); err != nil {
		return err
	}
	model := ""
	for _, f := range t.Files {
		root, _ := parseModelFile(f.Format, files[f.Path])
		raw, _ := json.Marshal(root)
		for field, expected := range f.Selectors {
			if gjson.GetBytes(raw, field).String() != expected {
				return ClassifiedError("model_config_mapping_missing")
			}
		}
		for field, variable := range f.Bindings {
			value := gjson.GetBytes(raw, field)
			if !value.Exists() || value.Type != gjson.String || strings.TrimSpace(value.String()) == "" {
				return ClassifiedError("model_config_mapping_missing")
			}
			switch variable {
			case "base_url":
				u, err := url.Parse(value.String())
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
					return ClassifiedError("model_config_invalid")
				}
			case "model_id":
				current := value.String()
				if (t.RuntimeKind == "openclaw" && field == "agents.defaults.model.primary") || (t.RuntimeKind == "omp" && field == "modelRoles.default") {
					current = strings.TrimPrefix(current, "multica/")
				}
				if model != "" && current != model {
					return ClassifiedError("model_config_invalid")
				}
				model = current
			}
		}
	}
	return nil
}

// Only native provider selectors from the template are retained. This metadata
// contains no credential or rendered file content and is pinned on first save.
func modelProviderSelectors(kind string, file ModelConfigFile, root map[string]any) map[string]string {
	if len(file.Bindings) == 0 {
		return nil
	}
	raw, _ := json.Marshal(root)
	paths := []string{}
	get := func(p string) string { return gjson.GetBytes(raw, p).String() }
	switch kind {
	case "codex":
		paths = []string{"model_provider"}
		if provider := get("model_provider"); provider != "" {
			paths = append(paths, "model_providers."+provider+".wire_api")
		}
	case "opencode":
		paths = []string{"model"}
		provider, _, _ := strings.Cut(get("model"), "/")
		if provider != "" {
			paths = append(paths, "provider."+provider+".npm")
		}
	case "grok":
		paths = []string{"models.default"}
		if model := get("models.default"); model != "" {
			paths = append(paths, "model."+model+".api_backend")
		}
	case "kimi":
		paths = []string{"default_model"}
		if model := get("default_model"); model != "" {
			p := "models." + model + ".provider"
			paths = append(paths, p)
			if provider := get(p); provider != "" {
				paths = append(paths, "providers."+provider+".type")
			}
		}
	case "pi":
		paths = []string{"defaultProvider"}
	}
	out := map[string]string{}
	for _, p := range paths {
		if value := gjson.GetBytes(raw, p); file.Bindings[p] == "" && value.Exists() && value.Type == gjson.String {
			out[p] = value.String()
		}
	}
	return out
}
