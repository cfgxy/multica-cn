package promquery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientDisabledWithoutURL(t *testing.T) {
	c := NewClient("")
	if c.Enabled() {
		t.Fatal("empty URL must be disabled")
	}
	var nilClient *Client
	if nilClient.Enabled() {
		t.Fatal("nil client must be disabled")
	}
	if _, err := nilClient.QueryRange(context.Background(), RangeQuery{}); err == nil {
		t.Fatal("disabled client must error, not return data")
	}
}

func TestQueryRangeParsesMatrix(t *testing.T) {
	var gotQuery, gotStart, gotStep string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotQuery, gotStart, gotStep = q.Get("query"), q.Get("start"), q.Get("step")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"daemon":"host-1","mode":"idle"},"values":[[1760000000,"12.5"],[1760000015,"13.5"]]},
			{"metric":{"daemon":"host-2"},"values":[[1760000000,"+Inf"]]}
		]}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	start := time.Unix(1760000000, 0)
	series, err := c.QueryRange(context.Background(), RangeQuery{
		Query: "up", Start: start, End: start.Add(time.Minute), Step: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "up" || gotStart != "1760000000" || gotStep != "15s" {
		t.Fatalf("request params mismatch: query=%q start=%q step=%q", gotQuery, gotStart, gotStep)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 series, got %d", len(series))
	}
	s0 := series[0]
	if s0.Labels["daemon"] != "host-1" || len(s0.Points) != 2 {
		t.Fatalf("series 0 mismatch: %+v", s0)
	}
	if s0.Points[1].Time != 1760000015 || s0.Points[1].Value != 13.5 {
		t.Fatalf("point mismatch: %+v", s0.Points[1])
	}
	// A non-numeric value ("+Inf") drops the sample but keeps the series.
	if s1 := series[1]; len(s1.Points) != 0 {
		t.Fatalf("expected +Inf sample to be dropped, got %+v", s1.Points)
	}
}

func TestQueryRangeSurfacesPrometheusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error at char 1"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.QueryRange(context.Background(), RangeQuery{Query: "bad{"})
	if err == nil || !strings.Contains(err.Error(), "parse error at char 1") {
		t.Fatalf("expected Prometheus error text, got %v", err)
	}
}

func TestEscapeLabelValue(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:  `plain`,
		`a"b`:    `a\"b`,
		`a\b`:    `a\\b`,
		"line\n": `line\n`,
	} {
		if got := EscapeLabelValue(in); got != want {
			t.Fatalf("EscapeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}
