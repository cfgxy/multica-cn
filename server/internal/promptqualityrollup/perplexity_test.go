package promptqualityrollup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptperplexity"
)

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	id, err := util.ParseUUID(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}

// stubGenerator records what it was asked and replies with a fixed score.
type stubGenerator struct {
	enabled bool
	reply   string
	err     error

	calls int
	users []string
}

func (s *stubGenerator) Enabled() bool        { return s.enabled }
func (s *stubGenerator) DefaultModel() string { return "stub-model" }

func (s *stubGenerator) GenerateJSON(_ context.Context, _, _, userPrompt string, _ float64, _ int64) (string, error) {
	s.calls++
	s.users = append(s.users, userPrompt)
	return s.reply, s.err
}

// scoreReply is a complete seven-item response. Written out rather than built
// from SubDimensions so the test would notice a silent change to the contract.
func scoreReply() string {
	items := make([]map[string]any, 0, len(promptperplexity.SubDimensions))
	for _, sd := range promptperplexity.SubDimensions {
		items = append(items, map[string]any{
			"key":           sd.Key,
			"score":         0.8,
			"justification": "stub judgement",
			"evidence": []map[string]string{
				{"tier": "Agent Identity", "section": "STOP / NEVER", "note": "stub"},
			},
		})
	}
	body, _ := json.Marshal(map[string]any{
		"band": "medium", "percent_low": 30, "percent_high": 45, "items": items,
	})
	return string(body)
}

type perplexityScenario struct {
	f       *testutil.Fixture
	t       *testing.T
	agentID string
	gen     *stubGenerator
	p       Perplexity
}

func newPerplexityScenario(t *testing.T) *perplexityScenario {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	runtimeID := f.Runtime(t, "pp-runtime")
	gen := &stubGenerator{enabled: true, reply: scoreReply()}
	return &perplexityScenario{
		f:       f,
		t:       t,
		agentID: f.Agent(t, "pp-agent", runtimeID),
		gen:     gen,
		p:       Perplexity{Queries: db.New(testPool), Generator: gen},
	}
}

// version writes an agent prompt version and returns its number.
func (s *perplexityScenario) version(n int, content string) int32 {
	s.t.Helper()
	s.f.Insert(s.t, "prompt_version", testutil.Cols{
		"workspace_id":   testWorkspaceID,
		"scope":          "agent",
		"scope_id":       s.agentID,
		"version":        n,
		"content":        content,
		"content_sha256": strings.Repeat("0", 64),
		"source":         "edit",
	})
	return int32(n)
}

func (s *perplexityScenario) rows() []db.PromptPerplexityScore {
	s.t.Helper()
	got, err := s.p.Queries.ListPromptPerplexityScores(context.Background(), db.ListPromptPerplexityScoresParams{
		Scope:   "agent",
		ScopeID: mustUUID(s.t, s.agentID),
	})
	if err != nil {
		s.t.Fatalf("list scores: %v", err)
	}
	s.t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM prompt_perplexity_score WHERE scope = 'agent' AND scope_id = $1`, s.agentID)
	})
	return got
}

func TestScoreAgentVersionStoresOneRowPerRuntimeProfile(t *testing.T) {
	s := newPerplexityScenario(t)
	v := s.version(3, "## Agent Identity\nDo the thing.")

	outcomes, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v)
	if err != nil {
		t.Fatalf("ScoreAgentVersion: %v", err)
	}
	for _, o := range outcomes {
		if o.Err != nil {
			t.Fatalf("profile %s: %v", o.Profile, o.Err)
		}
	}

	rows := s.rows()
	if len(rows) != len(promptperplexity.Profiles) {
		t.Fatalf("stored %d rows, want one per runtime profile (%d)", len(rows), len(promptperplexity.Profiles))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.RuntimeProfile] = true
		if r.Band != "medium" {
			t.Errorf("%s band = %q, want the band the model returned", r.RuntimeProfile, r.Band)
		}
		if r.Model != "stub-model" {
			t.Errorf("%s model = %q, want the model that produced it", r.RuntimeProfile, r.Model)
		}
	}
	for _, p := range promptperplexity.Profiles {
		if !seen[string(p)] {
			t.Errorf("no row for runtime profile %q", p)
		}
	}
}

// Owner Q17: the two profiles are separate measurements. If they were scored
// against the same document, storing two rows would be fiction.
func TestScoreAgentVersionScoresEachProfileAgainstItsOwnDocument(t *testing.T) {
	s := newPerplexityScenario(t)
	squadID := s.f.Squad(t, "pp-squad", s.agentID)
	s.f.Exec(t, `UPDATE squad SET instructions = $2 WHERE id = $1`, squadID, "## Squad Instructions\nLeader routes.")
	s.f.SquadMember(t, squadID, "agent", s.agentID)
	v := s.version(4, "## Agent Identity\nDo the thing.")

	if _, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v); err != nil {
		t.Fatalf("ScoreAgentVersion: %v", err)
	}
	s.rows()

	if s.gen.calls != len(promptperplexity.Profiles) {
		t.Fatalf("model called %d times, want once per profile", s.gen.calls)
	}
	if s.gen.users[0] == s.gen.users[1] {
		t.Error("both profiles were scored against the same assembled document")
	}
	leaderDoc := s.gen.users[1]
	if !strings.Contains(leaderDoc, "Leader routes.") {
		t.Error("the leader_task document omits the squad tier it actually receives")
	}
	if strings.Contains(s.gen.users[0], "Leader routes.") {
		t.Error("the member document carries a squad tier a plain member never reads")
	}
}

// An unconfigured model must leave the table empty. A row with a neutral band
// would put a number on the D3 card that no model produced.
func TestScoreAgentVersionWritesNothingWhenTheModelIsOff(t *testing.T) {
	s := newPerplexityScenario(t)
	s.gen.enabled = false
	v := s.version(5, "## Agent Identity\nDo the thing.")

	_, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v)
	if !errors.Is(err, promptperplexity.ErrNotScored) {
		t.Fatalf("err = %v, want ErrNotScored", err)
	}
	if rows := s.rows(); len(rows) != 0 {
		t.Errorf("stored %d rows for an unscored version", len(rows))
	}
}

// A refusal is per profile and must not be stored, but it must also not stop
// the profile that did score.
func TestScoreAgentVersionRecordsARefusalWithoutStoringIt(t *testing.T) {
	s := newPerplexityScenario(t)
	s.gen.reply = `{"band":"medium","percent_low":30,"percent_high":45,"items":[]}`
	v := s.version(6, "## Agent Identity\nDo the thing.")

	outcomes, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v)
	if err != nil {
		t.Fatalf("ScoreAgentVersion: %v", err)
	}
	if len(outcomes) != len(promptperplexity.Profiles) {
		t.Fatalf("got %d outcomes, want one per profile", len(outcomes))
	}
	for _, o := range outcomes {
		if o.Err == nil {
			t.Errorf("profile %s accepted a reply with no sub-dimensions", o.Profile)
		}
	}
	if rows := s.rows(); len(rows) != 0 {
		t.Errorf("stored %d rows from a malformed reply", len(rows))
	}
}

// The evidence column holds locations and justifications. The prompt body must
// never reach it (RUYI-184 source-B rule).
func TestScoreAgentVersionStoresEvidenceWithoutThePromptBody(t *testing.T) {
	s := newPerplexityScenario(t)
	const secretLine = "Deploy to the staging cluster at 0300 UTC."
	v := s.version(7, "## Agent Identity\n"+secretLine)

	if _, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v); err != nil {
		t.Fatalf("ScoreAgentVersion: %v", err)
	}
	for _, r := range s.rows() {
		if strings.Contains(string(r.Evidence), secretLine) {
			t.Errorf("%s evidence carries a line copied out of the prompt", r.RuntimeProfile)
		}
		var items []promptperplexity.Item
		if err := json.Unmarshal(r.Evidence, &items); err != nil {
			t.Fatalf("%s evidence is not the item list: %v", r.RuntimeProfile, err)
		}
		if len(items) != len(promptperplexity.SubDimensions) {
			t.Errorf("%s stored %d items, want all seven sub-dimensions", r.RuntimeProfile, len(items))
		}
	}
}

// Re-scoring the same version overwrites its rows rather than accumulating a
// second opinion per run of the job.
func TestScoreAgentVersionRescoreOverwritesInPlace(t *testing.T) {
	s := newPerplexityScenario(t)
	v := s.version(8, "## Agent Identity\nDo the thing.")

	if _, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v); err != nil {
		t.Fatalf("first score: %v", err)
	}
	s.gen.reply = strings.Replace(scoreReply(), `"band":"medium"`, `"band":"high"`, 1)
	if _, err := s.p.ScoreAgentVersion(context.Background(), mustUUID(t, s.agentID), v); err != nil {
		t.Fatalf("rescore: %v", err)
	}

	rows := s.rows()
	if len(rows) != len(promptperplexity.Profiles) {
		t.Fatalf("stored %d rows after a rescore, want one per profile", len(rows))
	}
	for _, r := range rows {
		if r.Band != "high" {
			t.Errorf("%s band = %q, want the rescored band", r.RuntimeProfile, r.Band)
		}
	}
}

// daily writes one prompt_quality_daily row for this scenario's agent, which
// is what makes a version eligible for the backlog: D3 only scores versions
// that runs actually went through.
func (s *perplexityScenario) daily(version int32, finishedRuns int) {
	s.t.Helper()
	s.f.Insert(s.t, "prompt_quality_daily", testutil.Cols{
		"workspace_id":  testWorkspaceID,
		"scope":         "agent",
		"scope_id":      s.agentID,
		"version":       version,
		"day":           time.Now().UTC().Format("2006-01-02"),
		"finished_runs": finishedRuns,
	})
}

// A deployment with no MULTICA_LLM_* configuration runs this job on every
// tick. It has to cost nothing and report nothing rather than erroring, since
// an unscored D3 is a state the dashboard renders.
func TestScoreBacklogIsANoOpWhenTheModelIsOff(t *testing.T) {
	s := newPerplexityScenario(t)
	s.gen.enabled = false
	v := s.version(11, "## Agent Identity\nDo the thing.")
	s.daily(v, 5)

	out, err := s.p.ScoreBacklog(context.Background())
	if err != nil {
		t.Fatalf("ScoreBacklog: %v", err)
	}
	if out != (BacklogOutcome{}) {
		t.Errorf("outcome = %+v, want a zero pass", out)
	}
	if s.gen.calls != 0 {
		t.Errorf("model called %d times with the client disabled", s.gen.calls)
	}
	if rows := s.rows(); len(rows) != 0 {
		t.Errorf("stored %d rows with the client disabled", len(rows))
	}
}

func TestScoreBacklogScoresAVersionThatHasRunsBehindIt(t *testing.T) {
	s := newPerplexityScenario(t)
	v := s.version(12, "## Agent Identity\nDo the thing.")
	s.daily(v, 4)

	out, err := s.p.ScoreBacklog(context.Background())
	if err != nil {
		t.Fatalf("ScoreBacklog: %v", err)
	}
	if out.Considered != 1 || out.Scored != 1 || out.Failed != 0 {
		t.Fatalf("outcome = %+v, want one version considered and scored", out)
	}
	if rows := s.rows(); len(rows) != len(promptperplexity.Profiles) {
		t.Errorf("stored %d rows, want one per runtime profile", len(rows))
	}
}

// A version nobody ran costs a model call for a card the dashboard never
// shows, so it must not enter the backlog at all.
func TestScoreBacklogIgnoresAVersionWithNoFinishedRuns(t *testing.T) {
	s := newPerplexityScenario(t)
	v := s.version(13, "## Agent Identity\nDo the thing.")
	s.daily(v, 0)

	out, err := s.p.ScoreBacklog(context.Background())
	if err != nil {
		t.Fatalf("ScoreBacklog: %v", err)
	}
	if out.Considered != 0 {
		t.Errorf("considered %d versions, want none: the version has no finished runs", out.Considered)
	}
	if s.gen.calls != 0 {
		t.Errorf("model called %d times for a version with no runs", s.gen.calls)
	}
	s.rows()
}

// Every tick re-lists the backlog. A version already scored for both profiles
// must drop out of it, or the job would rescore the same version forever and
// starve the ones still waiting.
func TestScoreBacklogLeavesAFullyScoredVersionAlone(t *testing.T) {
	s := newPerplexityScenario(t)
	v := s.version(14, "## Agent Identity\nDo the thing.")
	s.daily(v, 4)

	if _, err := s.p.ScoreBacklog(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	callsAfterFirst := s.gen.calls

	out, err := s.p.ScoreBacklog(context.Background())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if out.Considered != 0 {
		t.Errorf("considered %d versions on the second pass, want none", out.Considered)
	}
	if s.gen.calls != callsAfterFirst {
		t.Errorf("model called %d more times for an already scored version", s.gen.calls-callsAfterFirst)
	}
	s.rows()
}

// The backlog is a queue, not a transaction: a version whose content cannot be
// read is reported and skipped, and the versions behind it still get scored.
func TestScoreBacklogKeepsGoingAfterOneVersionFails(t *testing.T) {
	s := newPerplexityScenario(t)
	// 16 has a rollup row but no prompt_version row, so reading its content
	// fails. It sorts ahead of 15, which is the case that matters.
	s.daily(16, 4)
	v := s.version(15, "## Agent Identity\nDo the thing.")
	s.daily(v, 4)

	out, err := s.p.ScoreBacklog(context.Background())
	if err != nil {
		t.Fatalf("ScoreBacklog: %v", err)
	}
	if out.Considered != 2 || out.Scored != 1 || out.Failed != 1 {
		t.Fatalf("outcome = %+v, want the broken version failed and the other scored", out)
	}

	rows := s.rows()
	if len(rows) != len(promptperplexity.Profiles) {
		t.Fatalf("stored %d rows, want one per profile for the version that worked", len(rows))
	}
	for _, r := range rows {
		if r.Version != v {
			t.Errorf("stored a score for version %d, want %d", r.Version, v)
		}
	}
}
