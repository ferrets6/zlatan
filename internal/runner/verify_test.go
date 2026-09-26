// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"slices"
	"strings"
	"testing"
)

func TestParseCombined(t *testing.T) {
	report := strings.Join([]string{
		"= a/one.txt",
		"= b/two.txt",
		"* c/differ.txt",
		"+ d/missing.txt",
		"! e/error.txt",
		"- f/extra-in-nextcloud.txt",
		"",
	}, "\n")
	checked, bad, matched := parseCombined(report)
	// '=', '*', '+' and '!' are compared; '-' is not.
	if checked != 5 {
		t.Errorf("checked = %d, want 5", checked)
	}
	// '*', '+' and '!' are failures; '-' is not.
	if bad != 3 {
		t.Errorf("bad = %d, want 3", bad)
	}
	if !slices.Equal(matched, []string{"a/one.txt", "b/two.txt"}) {
		t.Errorf("matched = %v, want the two '=' paths", matched)
	}
}

func TestParseCombinedIgnoresNoise(t *testing.T) {
	report := "NOTICE: something\n\nx\n= a\n"
	checked, bad, matched := parseCombined(report)
	if checked != 1 || bad != 0 || len(matched) != 1 {
		t.Errorf("parseCombined = %d/%d/%v, want 1/0/[a]", checked, bad, matched)
	}
}

func TestParseImmichReport(t *testing.T) {
	report := `Asset Tracking Report:
=====================
Total Assets:       1234  (5.6 GiB)
  Processed:        1230  (5.5 GiB)
  Discarded:           4  (0.1 GiB)
  Errors:              2  (0 B)
  Pending:             1  (0 B)
`
	processed, discarded, errs, pending := parseImmichReport(report)
	if processed != 1230 || discarded != 4 || errs != 2 || pending != 1 {
		t.Errorf("parseImmichReport = %d/%d/%d/%d, want 1230/4/2/1",
			processed, discarded, errs, pending)
	}
}

func TestParseImmichReportEmpty(t *testing.T) {
	processed, discarded, errs, pending := parseImmichReport("")
	if processed != 0 || discarded != 0 || errs != 0 || pending != 0 {
		t.Errorf("an empty report should parse to zeros, got %d/%d/%d/%d",
			processed, discarded, errs, pending)
	}
}

func TestPickReturnsDistinctFiles(t *testing.T) {
	files := []string{"a", "b", "c", "d", "e"}
	got := pick(files, 3)
	if len(got) != 3 {
		t.Fatalf("pick returned %d files, want 3", len(got))
	}
	seen := map[string]bool{}
	for _, f := range got {
		if seen[f] {
			t.Errorf("pick returned %q twice", f)
		}
		seen[f] = true
	}
}

func TestPickMoreThanAvailable(t *testing.T) {
	files := []string{"a", "b"}
	got := pick(files, 5)
	if len(got) != 2 {
		t.Errorf("pick should return everything when n exceeds the list, got %d", len(got))
	}
}
