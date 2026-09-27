// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/oauth"
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

// The Drive copy deliberately leaves the Takeout folder behind: on the "Add to
// Drive" route the photo export lives in the person's Drive, and copying it
// into Nextcloud as files is wrong. The verification must leave it behind too,
// or it reports the exclusion itself as a file missing on the destination: a
// mismatch that no retry can clear, because every retry excludes it again.
// This is the bug that made "Retry" loop forever on "1 files that did not
// match".
func TestVerificationExcludesTheTakeoutFolder(t *testing.T) {
	store := newFakeStore()
	var checked []string
	exec := &fakeExecutor{
		byCommand: map[string]scripted{
			"check": {lines: []string{"= report.docx", "= photo.jpg"}},
		},
		onRun: func(args []string) {
			if len(args) == 0 || args[0] != "check" {
				return
			}
			for i, a := range args {
				if a == "--exclude" && i+1 < len(args) {
					checked = append(checked, args[i+1])
				}
			}
		},
	}
	r := newRunner(t, store, exec)

	if _, err := r.VerifyDrive(context.Background(), "marco",
		oauth.Tokens{AccessToken: "a", RefreshToken: "r"}, nextcloud.Credentials{LoginName: "marco"}); err != nil {
		t.Fatalf("VerifyDrive: %v", err)
	}

	if !slices.Contains(checked, "/Takeout/**") {
		t.Errorf("the size check must exclude the Takeout folder like the copy does, got %v", checked)
	}
}

// The copy and the check must share one exclusion list, so a folder the copy
// leaves behind can never be reported by the check as missing. This is what
// keeps the two from drifting apart again.
func TestCopyAndCheckShareTheExclusionList(t *testing.T) {
	r := newRunner(t, newFakeStore(), &fakeExecutor{})
	got := r.excludeTakeout()
	if !slices.Equal(got, []string{"/Takeout/**"}) {
		t.Errorf("excludeTakeout = %v, want [/Takeout/**]", got)
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

// A native Google document is exported, not copied, and its export is not
// reproducible: a byte comparison would report a difference that is not there
// and stop a migration whose data is intact. It must be kept out of the sample,
// while still being counted in the size pass.
func TestNativeGoogleDocsAreExcludedFromTheByteSample(t *testing.T) {
	store := newFakeStore()
	// The byte pass answers by file name, not with a fixed line: a document
	// would differ if it were sampled, and a photo would not. That is what
	// makes the test prove the exclusion rather than the fake's fixed output.
	var sampled []string
	exec := &fakeExecutor{
		byCommand: map[string]scripted{
			// The size pass: both files match by size.
			"check": {lines: []string{"= report.docx", "= photo.jpg"}},
			// The source listing marks report.docx as a native Google Doc.
			"lsjson": {lines: []string{
				`[{"Path":"report.docx","MimeType":"application/vnd.google-apps.document"},` +
					`{"Path":"photo.jpg","MimeType":"image/jpeg"}]`,
			}},
		},
		hasDownload: true,
		onRun: func(args []string) {
			// The --files-from list is a temp file; read it while it exists.
			for i, a := range args {
				if a == "--files-from" && i+1 < len(args) {
					raw, err := os.ReadFile(args[i+1])
					if err == nil {
						sampled = strings.Fields(string(raw))
					}
				}
			}
		},
	}
	r := newRunner(t, store, exec)

	v, err := r.VerifyDrive(context.Background(), "marco",
		oauth.Tokens{AccessToken: "a", RefreshToken: "r"}, nextcloud.Credentials{LoginName: "marco"})
	if err != nil {
		t.Fatalf("VerifyDrive: %v", err)
	}

	// The native document must not have been handed to the byte pass.
	if slices.Contains(sampled, "report.docx") {
		t.Errorf("a native Google document must not be byte-compared, sampled=%v", sampled)
	}
	if !slices.Contains(sampled, "photo.jpg") {
		t.Errorf("an ordinary file should still be byte-compared, sampled=%v", sampled)
	}
	if v.Mismatch != 0 {
		t.Errorf("a native Google document must not count as a mismatch, got %+v", v)
	}
	if !strings.Contains(v.Detail, "Google documents compared by size only") {
		t.Errorf("the detail should say the native documents were not byte-compared, got %q", v.Detail)
	}
	if v.Checked < 2 {
		t.Errorf("both files should still be size-checked, got Checked=%d", v.Checked)
	}
}
