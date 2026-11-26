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

func TestNextScheduleUsesFutureLocalBoundary(t *testing.T) {
	cases := []struct {
		name    string
		now     time.Time
		cadence string
		zone    string
		want    string
	}{
		// Saving daily mid-window must not make its current window eligible.
		{"daily mid-day", time.Date(2026, time.November, 26, 10, 15, 0, 0, time.UTC), "daily", "UTC", "2026-11-27T00:00:00Z"},
		{"weekly Monday", time.Date(2026, time.November, 23, 10, 15, 0, 0, time.UTC), "weekly", "UTC", "2026-11-30T00:00:00Z"},
		// February is short: the monthly boundary is the first of March, not a
		// synthetic February 30th. New York's spring DST change does not alter
		// a local-midnight daily anchor.
		{"short month", time.Date(2026, time.February, 28, 23, 0, 0, 0, time.UTC), "monthly", "UTC", "2026-03-01T00:00:00Z"},
		{"DST", time.Date(2026, time.March, 8, 6, 30, 0, 0, time.UTC), "daily", "America/New_York", "2026-03-09T04:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextSchedule(tc.now, tc.cadence, tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			if got.UTC().Format(time.RFC3339) != tc.want {
				t.Fatalf("NextSchedule() = %s; want %s", got.UTC().Format(time.RFC3339), tc.want)
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
