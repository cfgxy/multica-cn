package main

import "testing"

func TestValidateDecisionOptions(t *testing.T) {
	validOpts := []string{"Approve", "Reject", "Needs info"}

	cases := []struct {
		name        string
		question    string
		options     []string
		recommended []int
		wantErr     bool
	}{
		{"two options ok", "Ship now?", []string{"Yes", "No"}, nil, false},
		{"four options ok", "Ship now?", []string{"A", "B", "C", "D"}, []int{0, 2}, false},
		{"one option rejected", "Ship now?", []string{"Yes"}, nil, true},
		{"five options rejected", "Ship now?", []string{"A", "B", "C", "D", "E"}, nil, true},
		{"empty question rejected", "", validOpts, nil, true},
		{"empty label rejected", "Q", []string{"A", ""}, nil, true},
		{"duplicate labels rejected", "Q", []string{"A", "A"}, nil, true},
		{"recommendation out of range rejected", "Q", validOpts, []int{3}, true},
		{"negative recommendation rejected", "Q", validOpts, []int{-1}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDecisionOptions(tc.question, tc.options, tc.recommended)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateDecisionOptions(%q, %v, %v) error = %v, wantErr %v",
					tc.question, tc.options, tc.recommended, err, tc.wantErr)
			}
		})
	}
}
