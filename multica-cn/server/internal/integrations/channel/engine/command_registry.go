package engine

import "strings"

// CommandID identifies a registered channel slash command.
type CommandID string

const (
	CommandHelp  CommandID = "help"
	CommandNew   CommandID = "new"
	CommandIssue CommandID = "issue"
)

// CommandDescriptor describes one registered channel slash command. Name is
// the canonical slash token; Summary is the one-line description the /help
// card renders next to it; Listed controls inclusion on the card (a
// registered-but-unlisted command classifies as known without being
// advertised). RUYI-461: /help is itself a registered command — the card is
// rendered from this same registry, so registering a command is the only step
// needed to document it.
type CommandDescriptor struct {
	ID      CommandID
	Name    string
	Summary string
	Listed  bool
}

// helpCommandName is the canonical /help token. Router classifies it before
// any session write so asking for the menu never rotates a chat route.
const helpCommandName = "/help"

// CommandRegistry is the single source of truth for channel slash commands.
// Three consumers read it: Router classifies the first non-empty line's
// leading token through Lookup (unknown commands answer with /help guidance),
// the /help outcome renders ListedCommands, and future command dispatch
// (e.g. /stop) maps through the same table. Parsing precision is shared too —
// LeadingSlashToken below is the classification twin of parseLeadingCommand
// (/new, /clear, /issue all share it), so a body that classifies as a command
// for one consumer classifies the same for every other.
//
// Note /clear and /new remain parsed by ParseControlCommand upstream of the
// registry (session-control semantics predate it); the registry carries the
// commands that need classification (help) or card-button validation (new,
// issue).
type CommandRegistry struct {
	byName map[string]CommandDescriptor
	listed []CommandDescriptor
}

// NewCommandRegistry builds a registry from the supplied descriptors. Two
// descriptors sharing a name is a programming error and fails construction —
// silent aliasing would make the help card and the classifier disagree.
func NewCommandRegistry(cmds []CommandDescriptor) (*CommandRegistry, error) {
	r := &CommandRegistry{byName: make(map[string]CommandDescriptor, len(cmds))}
	for _, cmd := range cmds {
		if cmd.Name == "" {
			return nil, errDuplicateCommand("empty command name")
		}
		if _, dup := r.byName[cmd.Name]; dup {
			return nil, errDuplicateCommand(cmd.Name)
		}
		r.byName[cmd.Name] = cmd
		if cmd.Listed {
			r.listed = append(r.listed, cmd)
		}
	}
	return r, nil
}

type commandDuplicateError string

func errDuplicateCommand(name string) error { return commandDuplicateError(name) }

func (e commandDuplicateError) Error() string {
	return "command registry: duplicate command " + string(e)
}

// defaultCommands is the phase-1 set: the commands that already have product
// behavior today. The future /stop registers here (one descriptor) plus its
// dispatch branch in Router.processClaimed — the registry entry alone is what
// keeps /stop out of the unknown-command guidance once its dispatch lands.
var defaultCommands = []CommandDescriptor{
	{ID: CommandHelp, Name: "/help", Summary: "查看可用命令", Listed: true},
	{ID: CommandNew, Name: "/new", Summary: "新建一个对话，丢弃之前的上下文", Listed: true},
	{ID: CommandIssue, Name: "/issue", Summary: "从消息快速创建任务，例如 /issue 修复登录页", Listed: true},
}

// DefaultCommandRegistry returns the built-in command set shared by every
// channel adapter that enables slash-command feedback.
func DefaultCommandRegistry() *CommandRegistry {
	r, err := NewCommandRegistry(defaultCommands)
	if err != nil {
		// Unreachable for the fixed default table; a duplicate there is a
		// compile-time-constant bug and must fail loudly at boot.
		panic(err)
	}
	return r
}

// Lookup resolves a slash token (e.g. "/help") to its descriptor.
// Matching is exact and case-sensitive, matching the /issue and /new
// parsers: "/Help" is not "/help".
func (r *CommandRegistry) Lookup(name string) (CommandDescriptor, bool) {
	cmd, ok := r.byName[name]
	return cmd, ok
}

// ListedCommands returns the descriptors the /help card renders, in
// registration order.
func (r *CommandRegistry) ListedCommands() []CommandDescriptor {
	return append([]CommandDescriptor(nil), r.listed...)
}

// LeadingSlashToken extracts the slash token opening the first non-empty
// line of body ("/help me" → "/help"). Precision mirrors parseLeadingCommand:
// leading blank lines are skipped, the first non-empty line is the only
// candidate, leading spaces/tabs are tolerated, the token ends at the first
// space or tab, and matching is case-sensitive. ok=false means the body does
// not open with a slash token and no command classification applies.
func LeadingSlashToken(body string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "/") {
			return "", false
		}
		token := trimmed
		if cut := strings.IndexAny(trimmed, " \t"); cut >= 0 {
			token = trimmed[:cut]
		}
		return token, true
	}
	return "", false
}
