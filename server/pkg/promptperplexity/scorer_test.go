package promptperplexity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeGenerator struct {
	enabled bool
	model   string
	reply   string
	err     error

	calls   int
	systems []string
	users   []string
}

func (f *fakeGenerator) Enabled() bool        { return f.enabled }
func (f *fakeGenerator) DefaultModel() string { return f.model }

func (f *fakeGenerator) GenerateJSON(_ context.Context, _, systemPrompt, userPrompt string, _ float64, _ int64) (string, error) {
	f.calls++
	f.systems = append(f.systems, systemPrompt)
	f.users = append(f.users, userPrompt)
	return f.reply, f.err
}

// fakeCredentialValue is a synthetic value shaped like a credential so the
// detector fires. It is not a real key and matches no issuer's format.
const fakeCredentialValue = "not-a-real-key-0000000000"

func okGenerator() *fakeGenerator {
	return &fakeGenerator{enabled: true, model: "test-model", reply: validScore()}
}

func TestScoreTiersReturnsTheParsedScoreAndTheModelThatProducedIt(t *testing.T) {
	gen := okGenerator()
	got, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Workspace: "W", Agent: "A"})
	if err != nil {
		t.Fatalf("ScoreTiers: %v", err)
	}
	if got.Model != "test-model" {
		t.Errorf("Model = %q, want the model that was actually called", got.Model)
	}
	if got.Score.Band != BandMedium {
		t.Errorf("Band = %q, want medium", got.Score.Band)
	}
	if got.Profile != ProfileMember {
		t.Errorf("Profile = %q, want member", got.Profile)
	}
}

// An unconfigured LLM must surface as "not scored". Returning a neutral band
// would put a number on the D3 card that no model produced, which is the exact
// failure mode the whole dimension is built to avoid.
func TestScoreTiersRefusesToInventAScoreWhenTheModelIsOff(t *testing.T) {
	gen := &fakeGenerator{enabled: false}
	_, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Workspace: "W"})
	if !errors.Is(err, ErrNotScored) {
		t.Fatalf("err = %v, want ErrNotScored", err)
	}
	if gen.calls != 0 {
		t.Errorf("called the model %d times while disabled", gen.calls)
	}
}

// Nothing to score is also not a zero score.
func TestScoreTiersRefusesAnEmptyPrompt(t *testing.T) {
	gen := okGenerator()
	_, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{})
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("err = %v, want ErrNoContent", err)
	}
	if gen.calls != 0 {
		t.Errorf("sent an empty prompt to the model %d times", gen.calls)
	}
}

// A member profile carrying only a squad tier has nothing it would actually
// read, so it is "no content" rather than a prompt assembled from a tier the
// runtime withholds.
func TestScoreTiersTreatsASquadOnlyMemberAsNoContent(t *testing.T) {
	gen := okGenerator()
	_, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Squad: "S"})
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("err = %v, want ErrNoContent", err)
	}
}

func TestScoreTiersRejectsAMalformedReplyWithoutSalvagingIt(t *testing.T) {
	gen := okGenerator()
	gen.reply = `{"band": "medium", "percent_low": 40, "percent_high": 55, "items": []}`
	if _, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Agent: "A"}); err == nil {
		t.Fatal("ScoreTiers accepted a reply with no sub-dimensions")
	}
}

func TestScoreTiersPropagatesAGenerationFailure(t *testing.T) {
	gen := okGenerator()
	gen.err = errors.New("upstream 503")
	if _, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Agent: "A"}); err == nil {
		t.Fatal("ScoreTiers swallowed an upstream failure")
	}
}

// Fail-closed: the assembled prompt is workspace-owned text that leaves the
// platform. If the credential detector fires on it, the request is refused
// rather than redacted — a redacted send still ships the surrounding rule text
// that a publisher has not reviewed for this purpose.
func TestScoreTiersRefusesToSendContentCarryingACredential(t *testing.T) {
	gen := okGenerator()
	tiers := Tiers{Agent: "deploy with api_key = " + fakeCredentialValue}
	_, err := ScoreTiers(context.Background(), gen, ProfileMember, tiers)
	if !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("err = %v, want ErrUnsafeContent", err)
	}
	if gen.calls != 0 {
		t.Errorf("sent credential-bearing content to the model %d times", gen.calls)
	}
}

// The refusal itself must not echo what it found.
func TestScoreTiersRefusalDoesNotEchoTheSecret(t *testing.T) {
	gen := okGenerator()
	const secret = fakeCredentialValue
	_, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Agent: "api_key = " + secret})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), secret) {
		t.Error("the refusal message echoed the detected value")
	}
}

// The word JSON has to appear in the prompt or OpenAI-compatible endpoints
// reject the request outright (pkg/llm/client.go).
func TestScoreTiersPromptSatisfiesTheJSONModeConstraint(t *testing.T) {
	gen := okGenerator()
	if _, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Agent: "A"}); err != nil {
		t.Fatalf("ScoreTiers: %v", err)
	}
	joined := gen.systems[0] + gen.users[0]
	if !strings.Contains(joined, "JSON") {
		t.Error("prompt omits the literal word JSON; json_object mode would be rejected upstream")
	}
}

// Every sub-dimension key the parser requires has to be named in the prompt,
// or the model is being asked to guess the contract it will be judged against.
func TestScoreTiersPromptNamesEverySubDimension(t *testing.T) {
	gen := okGenerator()
	if _, err := ScoreTiers(context.Background(), gen, ProfileMember, Tiers{Agent: "A"}); err != nil {
		t.Fatalf("ScoreTiers: %v", err)
	}
	joined := gen.systems[0] + gen.users[0]
	for _, sd := range SubDimensions {
		if !strings.Contains(joined, sd.Key) {
			t.Errorf("prompt never names sub-dimension %q", sd.Key)
		}
	}
}

// The two profiles are scored against different documents; if the same text
// went out twice, storing two rows would be fiction.
func TestScoreTiersSendsADifferentDocumentPerProfile(t *testing.T) {
	gen := okGenerator()
	tiers := Tiers{Workspace: "W", Squad: "S", Agent: "A"}
	if _, err := ScoreTiers(context.Background(), gen, ProfileMember, tiers); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := ScoreTiers(context.Background(), gen, ProfileLeaderTask, tiers); err != nil {
		t.Fatalf("leader_task: %v", err)
	}
	if gen.users[0] == gen.users[1] {
		t.Error("both profiles were scored against the same assembled prompt")
	}
}
