package engine

import "testing"

func TestDefaultCommandRegistry_ListsPhaseOneCommands(t *testing.T) {
	r := DefaultCommandRegistry()
	listed := r.ListedCommands()
	if len(listed) != 3 {
		t.Fatalf("expected 3 listed commands, got %d: %+v", len(listed), listed)
	}
	wantNames := map[string]bool{"/help": true, "/new": true, "/issue": true}
	for _, cmd := range listed {
		if !wantNames[cmd.Name] {
			t.Fatalf("unexpected listed command %q", cmd.Name)
		}
		if cmd.Summary == "" {
			t.Fatalf("listed command %q must carry a summary for the help card", cmd.Name)
		}
		delete(wantNames, cmd.Name)
	}
	if len(wantNames) != 0 {
		t.Fatalf("missing listed commands: %v", wantNames)
	}
}

func TestCommandRegistry_Lookup(t *testing.T) {
	r := DefaultCommandRegistry()
	for _, token := range []string{"/help", "/new", "/issue"} {
		if _, ok := r.Lookup(token); !ok {
			t.Fatalf("expected %q to be registered", token)
		}
	}
	for _, token := range []string{
		"/stop",   // future command: not registered in phase 1
		"/ISSUE",  // case-sensitive, matching the /issue parser
		"/Help",   // case-sensitive
		"/issuet", // token boundary: a longer token is not a registered one
		"issue",   // missing slash
		"",        // empty
	} {
		if _, ok := r.Lookup(token); ok {
			t.Fatalf("expected %q to be unregistered", token)
		}
	}
}

func TestNewCommandRegistry_RejectsDuplicateNames(t *testing.T) {
	if _, err := NewCommandRegistry([]CommandDescriptor{
		{ID: CommandHelp, Name: "/help"},
		{ID: CommandNew, Name: "/help"},
	}); err == nil {
		t.Fatal("expected duplicate-name construction to fail")
	}
	if _, err := NewCommandRegistry([]CommandDescriptor{{Name: ""}}); err == nil {
		t.Fatal("expected empty-name construction to fail")
	}
}

func TestLeadingSlashToken(t *testing.T) {
	cases := []struct {
		body  string
		token string
		ok    bool
	}{
		{"/help", "/help", true},
		{"/help me", "/help", true},
		{"/help\tme", "/help", true},
		{"  /help", "/help", true},
		{"\n\n  /issue 修复登录页\n描述", "/issue", true},
		{"/helpme", "/helpme", true},
		{"/ISSUE", "/ISSUE", true},
		{"hello /help", "", false},      // command must open the first non-empty line
		{"hi\n/help", "", false},        // a later line is not a command
		{"", "", false},                 // empty body
		{"\n \n\t\n", "", false},        // blank-only body
		{" / text", "/", true},          // a lone slash is still a slash token
		{"run /issue later", "", false}, // mid-line mention is prose
	}
	for _, tc := range cases {
		token, ok := LeadingSlashToken(tc.body)
		if ok != tc.ok || token != tc.token {
			t.Fatalf("LeadingSlashToken(%q) = (%q, %v), want (%q, %v)", tc.body, token, ok, tc.token, tc.ok)
		}
	}
}

// LeadingSlashToken must classify with exactly the precision the /issue and
// /new parsers use: token-bounded, case-sensitive, first non-empty line only.
// The registry classification and the control-command pipeline therefore
// never disagree about whether a body opens with a command.
func TestLeadingSlashToken_MatchesParseLeadingCommandPrecision(t *testing.T) {
	cases := []struct {
		body         string
		token        string
		productMatch bool // a product parser (control or /issue) claims this body
	}{
		{"/issue 修复登录页", "/issue", true},
		{"/issue", "/issue", true},
		{"/new", "/new", true},
		{"/new 帮我写周报", "/new", true},
		{"/clear", "/clear", true},
		{"/help", "/help", false},
		{"/helpme", "/helpme", false}, // token boundary: no product parser claims it
		{"/ISSUE x", "/ISSUE", false}, // case-sensitive: no product parser claims it
		{"/help 帮助", "/help", false},
		{"plain text", "", false},
		{"  /new", "/new", true},
		{"\n/new", "/new", true},
		{"/newextra", "/newextra", false},
		{"x /new", "", false},
	}
	for _, tc := range cases {
		token, ok := LeadingSlashToken(tc.body)
		if ok != (tc.token != "") || token != tc.token {
			t.Fatalf("LeadingSlashToken(%q) = (%q, %v), want (%q, %v)", tc.body, token, ok, tc.token, tc.token != "")
		}
		_, isControl := ParseControlCommand(tc.body)
		_, isIssue := ParseIssueCommand(tc.body)
		if (isControl || isIssue) != tc.productMatch {
			t.Fatalf("product-parser agreement failed on %q: control=%v issue=%v want match=%v", tc.body, isControl, isIssue, tc.productMatch)
		}
	}
}
