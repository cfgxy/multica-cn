package handler

// The licence enum is a cross-language contract (RUYI-100).
//
// The publish dialog offers a fixed list of licences and sends the chosen code
// verbatim; the server validates it against a closed set. The two lists are
// written in different languages and cannot be generated from one another, so
// nothing but a test stops them from drifting — and when they drift the only
// symptom is a 400 on the one licence the user picked, with a correct-looking
// dropdown and a correct-looking validator on either side of it.
//
// This test reads OUT of the Go module. CI's backend path filter therefore has
// to list the frontend files explicitly (`backend` in .github/workflows/ci.yml);
// without that, a PR that only edits the frontend list skips the very test that
// would catch the mismatch. Move or rename either path below and the filter
// must move with it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// promptLicenseFrontendType is packages/core/types/prompt-market.ts, the union
// the API client is typed against.
const promptLicenseFrontendType = "packages/core/types/prompt-market.ts"

// promptLicenseFrontendList is packages/views/market/prompt-market-labels.ts,
// the array the publish dialog actually renders as options.
const promptLicenseFrontendList = "packages/views/market/prompt-market-labels.ts"

// promptLicenseLocales are the four locales the i18n parity contract requires.
// A code with no label renders as itself — a raw slug in the dropdown — so the
// labels are part of the same contract rather than a cosmetic extra.
var promptLicenseLocales = []string{"en", "zh-Hans", "ja", "ko"}

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return src
}

func sortedLicenseCodes() []string {
	codes := make([]string, 0, len(promptLicenseCodes))
	for code := range promptLicenseCodes {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func TestPromptLicenseCodesMatchFrontendUnion(t *testing.T) {
	src := repoFile(t, promptLicenseFrontendType)
	block := regexp.MustCompile(`(?s)export type PromptLicenseCode\s*=(.*?);`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("PromptLicenseCode union not found in %s", promptLicenseFrontendType)
	}
	got := extractQuoted(block[1])
	assertSameCodes(t, promptLicenseFrontendType, got)
}

func TestPromptLicenseCodesMatchFrontendOptionList(t *testing.T) {
	src := repoFile(t, promptLicenseFrontendList)
	block := regexp.MustCompile(`(?s)const LICENSE_KEYS = \[(.*?)\]`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("LICENSE_KEYS not found in %s", promptLicenseFrontendList)
	}
	got := extractQuoted(block[1])
	assertSameCodes(t, promptLicenseFrontendList, got)
}

func TestPromptLicenseCodesHaveALabelInEveryLocale(t *testing.T) {
	for _, locale := range promptLicenseLocales {
		rel := "packages/views/locales/" + locale + "/prompt-market.json"
		var ns struct {
			License map[string]string `json:"license"`
		}
		if err := json.Unmarshal(repoFile(t, rel), &ns); err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		got := make([]string, 0, len(ns.License))
		for code := range ns.License {
			got = append(got, code)
		}
		sort.Strings(got)
		assertSameCodes(t, rel, got)
	}
}

// extractQuoted pulls every double-quoted string out of a TypeScript fragment.
// The fragments this runs on are a union and an array literal — both are lists
// of string literals and nothing else — so a quote-level parse is exact here
// without dragging a TS parser into the Go suite.
func extractQuoted(fragment []byte) []string {
	matches := regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(fragment, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, string(m[1]))
	}
	sort.Strings(out)
	return out
}

func assertSameCodes(t *testing.T, where string, got []string) {
	t.Helper()
	want := sortedLicenseCodes()
	if len(got) != len(want) {
		t.Fatalf("%s lists %v; the server accepts %v", where, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s lists %v; the server accepts %v — a user choosing %q would get a 400",
				where, got, want, got[i])
		}
	}
}
