package sub2api

// RuntimeProtocols describes the implemented native configuration adapters,
// not a per-key upstream ACL. Administrator grants remain the ownership boundary.
func RuntimeProtocols(kind string) []string {
	switch kind {
	case "claude":
		return []string{"anthropic-messages"}
	case "codex", "grok":
		return []string{"openai-responses"}
	case "opencode", "kimi":
		return []string{"openai-chat-completions"}
	case "omp", "pi", "openclaw":
		return []string{"openai-chat-completions", "anthropic-messages", "openai-responses"}
	}
	return nil
}

func platformProtocol(platform, protocol string) bool {
	return (platform == "anthropic" && protocol == "anthropic_messages") ||
		(platform == "openai" && (protocol == "openai_responses" || protocol == "openai_chat_completions"))
}

func CompatibleRuntimes(platform string, protocols []string) []string {
	available := []string{}
	for _, p := range protocols {
		if !platformProtocol(platform, p) {
			continue
		}
		switch p {
		case "anthropic_messages":
			available = append(available, "anthropic-messages")
		case "openai_responses":
			available = append(available, "openai-responses")
		case "openai_chat_completions":
			available = append(available, "openai-chat-completions")
		}
	}
	kinds := []string{}
	for _, kind := range []string{"claude", "codex", "opencode", "openclaw", "grok", "kimi", "omp", "pi"} {
		for _, p := range RuntimeProtocols(kind) {
			if Contains(available, p) {
				kinds = append(kinds, kind)
				break
			}
		}
	}
	return kinds
}
