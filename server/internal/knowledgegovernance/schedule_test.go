package knowledgegovernance

import (
	"testing"
	"time"
)

func TestScheduleBucketUsesWorkspaceLocalCalendar(t *testing.T) {
	utc := time.Date(2026, time.March, 1, 0, 30, 0, 0, time.UTC)
	cases := []struct {
		cadence string
		zone    string
		want    string
	}{
		{"daily", "America/New_York", "2026-02-28"},
		{"weekly", "UTC", "2026-W09"},
		{"monthly", "Asia/Shanghai", "2026-03"},
	}
	for _, tc := range cases {
		t.Run(tc.cadence+"/"+tc.zone, func(t *testing.T) {
			got, err := scheduleBucket(utc, tc.cadence, tc.zone)
			if err != nil || got != tc.want {
				t.Fatalf("scheduleBucket(%s, %s) = %q, %v; want %q", tc.cadence, tc.zone, got, err, tc.want)
			}
		})
	}
}

func TestScheduleBucketRejectsInvalidConfiguration(t *testing.T) {
	if _, err := scheduleBucket(time.Now(), "yearly", "UTC"); err == nil {
		t.Fatal("unsupported cadence must fail")
	}
	if _, err := scheduleBucket(time.Now(), "daily", "Mars/Olympus"); err == nil {
		t.Fatal("invalid timezone must fail")
	}
}
