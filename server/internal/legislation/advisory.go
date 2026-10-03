package legislation

// jev advisory client (RUYI-347 batch A): the warn-only soft-judgment
// sidecar backed by the local binary engine's pilot service. The client is
// read-only against a loopback service with no credentials, never logs the
// judged text, and never fails its caller — every fault (disabled,
// unreachable, timeout, 5xx) degrades to an unavailable AdvisoryReport that
// names the reason. Gate verdicts stay untouched: this layer reports, it
// never blocks.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Env knobs (MULTICA_ prefix per house convention). An empty URL disables
// the advisory layer entirely — that is the switch; the fail-closed gate
// semantics are identical either way.
const (
	DefaultJevAdvisoryURL       = "http://127.0.0.1:8321"
	DefaultJevAdvisoryTimeoutMS = 1500
	EnvJevAdvisoryURL           = "MULTICA_JEV_ADVISORY_URL"
	EnvJevAdvisoryTimeoutMS     = "MULTICA_JEV_ADVISORY_TIMEOUT_MS"
)

// AdvisoryClient judges one text against the jev binary engine. A nil
// client or an empty BaseURL reports "disabled".
type AdvisoryClient struct {
	BaseURL string
	Timeout time.Duration
}

type jevJudgeResponse struct {
	Task            string  `json:"task"`
	PSuccess        float64 `json:"p_success"`
	PFailure        float64 `json:"p_failure"`
	Threshold       float64 `json:"threshold"`
	Warn            bool    `json:"warn"`
	Decision        string  `json:"decision"`
	Engine          string  `json:"engine"`
	ThresholdSource string  `json:"threshold_source"`
}

// AdvisoryReport is the jsonb shape persisted on prompt_proposal.jev_advisory.
// Only the probability verdict and a digest of the input are stored — never
// the judged text itself.
type AdvisoryReport struct {
	Stage         string  `json:"stage"` // "submit" | "gate"
	Available     bool    `json:"available"`
	Engine        string  `json:"engine,omitempty"`
	PFailure      float64 `json:"p_failure,omitempty"`
	Threshold     float64 `json:"threshold,omitempty"`
	Warn          bool    `json:"warn"`
	Decision      string  `json:"decision,omitempty"`
	InputSHA256   string  `json:"input_sha256"`
	SkippedReason string  `json:"skipped_reason,omitempty"`
	CheckedAt     string  `json:"checked_at"`
}

// Judge posts one text to the service and returns the raw verdict.
func (c *AdvisoryClient) Judge(ctx context.Context, text string) (*jevJudgeResponse, error) {
	payload, err := json.Marshal(map[string]string{"input": text})
	if err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = time.Duration(DefaultJevAdvisoryTimeoutMS) * time.Millisecond
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/v1/binary/judge", strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	httpClient := &http.Client{Timeout: timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var out jevJudgeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Assess wraps Judge with the warn-only degrade contract: it never returns
// an error and never blocks the caller's flow. The input text is digested,
// not stored.
func (c *AdvisoryClient) Assess(ctx context.Context, stage, text string) AdvisoryReport {
	sum := sha256.Sum256([]byte(text))
	rep := AdvisoryReport{
		Stage:       stage,
		InputSHA256: hex.EncodeToString(sum[:]),
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		rep.SkippedReason = "disabled"
		return rep
	}
	if strings.TrimSpace(text) == "" {
		rep.SkippedReason = "empty_input"
		return rep
	}
	out, err := c.Judge(ctx, text)
	if err != nil {
		rep.SkippedReason = "unreachable: " + truncateRunes(err.Error(), 120)
		return rep
	}
	rep.Available = true
	rep.Engine = out.Engine
	rep.PFailure = out.PFailure
	rep.Threshold = out.Threshold
	rep.Warn = out.Warn
	rep.Decision = out.Decision
	return rep
}

// AdvisoryFromEnv builds the process-default client from the environment.
// The result is cached: env is read once per process, so tests must inject
// their own client instead of mutating env mid-run.
func AdvisoryFromEnv() *AdvisoryClient {
	jevAdvisoryOnce.Do(func() {
		url := strings.TrimSpace(os.Getenv(EnvJevAdvisoryURL))
		if url == "" {
			if _, set := os.LookupEnv(EnvJevAdvisoryURL); !set {
				url = DefaultJevAdvisoryURL
			}
		}
		timeoutMS := DefaultJevAdvisoryTimeoutMS
		if raw := strings.TrimSpace(os.Getenv(EnvJevAdvisoryTimeoutMS)); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				timeoutMS = n
			}
		}
		jevAdvisoryDefault = &AdvisoryClient{BaseURL: url, Timeout: time.Duration(timeoutMS) * time.Millisecond}
	})
	return jevAdvisoryDefault
}

var (
	jevAdvisoryOnce    sync.Once
	jevAdvisoryDefault *AdvisoryClient
)

// Enabled reports whether the client would actually call the service — a
// nil receiver or an empty BaseURL is the off switch.
func (c *AdvisoryClient) Enabled() bool {
	return c != nil && strings.TrimSpace(c.BaseURL) != ""
}

// JSON marshals the report for the jev_advisory column. It returns nil when
// the layer is disabled, so the column stays NULL (未检/关态) rather than
// carrying a placeholder report.
func (r AdvisoryReport) JSON() []byte {
	if !r.Available && r.SkippedReason == "disabled" {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil
	}
	return b
}
