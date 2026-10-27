package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeVerifiedAgentResultsQueries struct {
	rows []db.ListVerifiedAgentResultsForLarkContextRow
	err  error
	args []db.ListVerifiedAgentResultsForLarkContextParams
}

func (f *fakeVerifiedAgentResultsQueries) ListVerifiedAgentResultsForLarkContext(_ context.Context, arg db.ListVerifiedAgentResultsForLarkContextParams) ([]db.ListVerifiedAgentResultsForLarkContextRow, error) {
	f.args = append(f.args, arg)
	return f.rows, f.err
}

func TestVerifiedAgentResultsResolverScopesLookupAndOrdersWindowCandidates(t *testing.T) {
	installationID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	queries := &fakeVerifiedAgentResultsQueries{rows: []db.ListVerifiedAgentResultsForLarkContextRow{
		{MessageID: "om_later", AgentName: "Analyst", Content: "later"},
		{MessageID: "om_first", AgentName: "Researcher", Content: "first"},
	}}
	resolver := NewVerifiedAgentResultsResolver(queries)

	results, err := resolver.ResolveVerifiedAgentResults(context.Background(), VerifiedAgentResultsLookup{
		InstallationID: installationID, ConversationKey: "oc_group:thread_1",
		CandidateMessageIDs: []string{"om_first", "om_later"},
	})
	if err != nil {
		t.Fatalf("ResolveVerifiedAgentResults: %v", err)
	}
	if len(queries.args) != 1 {
		t.Fatalf("lookup calls = %d, want 1", len(queries.args))
	}
	arg := queries.args[0]
	if arg.InstallationID != installationID || arg.ChannelChatID.String != "oc_group:thread_1" || !equalStrings(arg.CandidateMessageIds, []string{"om_first", "om_later"}) {
		t.Errorf("query scope = %+v", arg)
	}
	if len(results) != 2 || results[0].MessageID != "om_first" || results[1].MessageID != "om_later" {
		t.Errorf("results = %+v, want candidate-window order", results)
	}
}

func TestVerifiedAgentResultsResolverDropsAmbiguousProvenance(t *testing.T) {
	queries := &fakeVerifiedAgentResultsQueries{rows: []db.ListVerifiedAgentResultsForLarkContextRow{
		{MessageID: "om_duplicate", AgentName: "Agent A", Content: "first"},
		{MessageID: "om_duplicate", AgentName: "Agent B", Content: "second"},
	}}
	resolver := NewVerifiedAgentResultsResolver(queries)

	results, err := resolver.ResolveVerifiedAgentResults(context.Background(), VerifiedAgentResultsLookup{
		InstallationID:      pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		ConversationKey:     "oc_group",
		CandidateMessageIDs: []string{"om_duplicate"},
	})
	if err != nil {
		t.Fatalf("ResolveVerifiedAgentResults: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("ambiguous provenance must be dropped, got %+v", results)
	}
}

func TestRenderVerifiedAgentResultsBlockCountsOnlyRenderedResults(t *testing.T) {
	content := strings.Repeat("x", verifiedAgentResultMaxChars)
	block := renderVerifiedAgentResultsBlock([]VerifiedAgentResult{
		{AgentName: "Agent 1", Content: content},
		{AgentName: "Agent 2", Content: content},
		{AgentName: "Agent 3", Content: content},
		{AgentName: "Agent 4", Content: content},
	})
	if !strings.Contains(block, `<verified_agent_results count="3">`) {
		t.Errorf("block count = %q, want 3 rendered results", block[:min(len(block), 100)])
	}
	if strings.Contains(block, "[Agent 4]") {
		t.Errorf("block contains result beyond total budget")
	}
}
