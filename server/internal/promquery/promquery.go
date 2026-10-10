// Package promquery is a minimal client for the Prometheus HTTP API
// (query_range only). The usage page's system-resource and model-traffic
// panels fetch their data through the server proxy endpoints, which run
// PromQL here — the browser never talks to Prometheus directly.
package promquery

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvURL is the environment variable holding the Prometheus base URL (e.g.
// http://prometheus:9090). Empty or unset disables every proxy endpoint.
const EnvURL = "PROMETHEUS_URL"

// Client queries one Prometheus server. A nil Client, or one built from an
// empty URL, is the "not configured" state: callers return a structured
// empty payload instead of an error, because a deployment without the
// observability stack is normal, not broken.
type Client struct {
	baseURL string
	http    *http.Client
	now     func() time.Time
}

// NewClient builds a client for base (trailing slashes trimmed). An empty
// base yields a disabled client (Enabled() == false).
func NewClient(base string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(base), "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
		now:     time.Now,
	}
}

// NewClientFromEnv builds a client from PROMETHEUS_URL.
func NewClientFromEnv() *Client {
	return NewClient(os.Getenv(EnvURL))
}

// Enabled reports whether the client has a base URL to query.
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

// Point is one sample: Unix seconds plus the float value Prometheus reported.
type Point struct {
	Time  int64   `json:"t"`
	Value float64 `json:"v"`
}

// Series is one labeled time series with its samples in time order.
type Series struct {
	Labels map[string]string `json:"labels"`
	Points []Point           `json:"points"`
}

// RangeQuery is one PromQL range evaluation.
type RangeQuery struct {
	Query string
	Start time.Time
	End   time.Time
	Step  time.Duration
}

type apiResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string   `json:"metric"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// QueryRange evaluates q and returns the matrix result. Prometheus returns
// sample timestamps as seconds and values as strings; both are normalized
// here so callers never touch the wire encoding.
func (c *Client) QueryRange(ctx context.Context, q RangeQuery) ([]Series, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("promquery: client is not configured")
	}
	params := url.Values{
		"query": {q.Query},
		"start": {strconv.FormatInt(q.Start.Unix(), 10)},
		"end":   {strconv.FormatInt(q.End.Unix(), 10)},
		"step":  {fmt.Sprintf("%.0fs", q.Step.Seconds())},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v1/query_range?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("promquery: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("promquery: query %q: %w", q.Query, err)
	}
	defer resp.Body.Close()
	var decoded apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("promquery: decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || decoded.Status != "success" {
		return nil, fmt.Errorf("promquery: query %q failed: http %d %s", q.Query, resp.StatusCode, decoded.Error)
	}
	series := make([]Series, 0, len(decoded.Data.Result))
	for _, r := range decoded.Data.Result {
		s := Series{Labels: r.Metric}
		for _, v := range r.Values {
			if len(v) != 2 {
				continue
			}
			var ts float64
			var raw string
			if err := json.Unmarshal(v[0], &ts); err != nil {
				continue
			}
			if err := json.Unmarshal(v[1], &raw); err != nil {
				continue
			}
			val, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsInf(val, 0) || math.IsNaN(val) {
				// Unparseable or non-finite ("+Inf" during counter warmup):
				// keep the series, drop the sample — charts cannot use it.
				continue
			}
			s.Points = append(s.Points, Point{Time: int64(ts), Value: val})
		}
		series = append(series, s)
	}
	return series, nil
}

// EscapeLabelValue renders v for use inside a PromQL string literal
// (`metric{label="..."}`), so hostnames containing quotes or backslashes
// cannot break out of the matcher.
func EscapeLabelValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(v)
}
