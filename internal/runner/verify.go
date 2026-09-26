// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/oauth"
)

// SampleSize is how many files are compared byte for byte. The whole tree is
// compared by size (cheap, catches anything missing or truncated); the sample
// is downloaded from Google and diffed, which is strong but costs bandwidth,
// so it stays small.
const SampleSize = 10

// VerifyDrive checks that what landed in Nextcloud matches the person's Drive.
//
// It is two checks, and the caller is told which ran:
//
//   - every file, by size, one way (source must exist on the destination).
//     This catches a file that never arrived or arrived truncated, and it is
//     cheap because nothing is downloaded.
//   - a random sample, byte for byte. Drive exposes MD5 and Nextcloud exposes
//     SHA1, so there is no hash both sides share and rclone cannot compare
//     hashes directly; downloading the sample is the only honest way to say
//     "these bytes are the same", so that is what it does, and only for a few
//     files.
//
// A size check cannot see silent corruption at equal size; the sample can, but
// only for the files in it. The result says exactly which was done, so the
// wizard never claims more than it did.
func (r *Runner) VerifyDrive(ctx context.Context, user string, tokens oauth.Tokens, creds nextcloud.Credentials) (core.Verify, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()

	env, err := r.rcloneEnv(tokens, creds)
	if err != nil {
		return core.Verify{}, err
	}
	src := "gdrive:"
	dst := "nc:" + DriveDestination

	// One-way: files in Nextcloud that are not in Drive are not a failure —
	// the person may have put things there themselves.
	sizeChecked, sizeBad, matched := r.checkTree(ctx, src, dst, env)

	sampleChecked, sampleBad, err := r.checkSample(ctx, src, dst, env, matched)
	if err != nil {
		// The sample is the stronger check but not the only one: if it cannot
		// run, report what the size pass found rather than failing the whole
		// verification.
		r.log.Warn("verify: sample check failed, reporting the size check only", "user", user, "error", err)
	}

	v := core.Verify{
		User:     user,
		Track:    core.TrackDrive,
		Checked:  sizeChecked + sampleChecked,
		Matched:  sizeChecked - sizeBad + sampleChecked - sampleBad,
		Mismatch: sizeBad + sampleBad,
	}
	v.Detail = fmt.Sprintf(
		"every file compared by size (%d), %d sampled and compared byte for byte",
		sizeChecked, sampleChecked)
	return v, nil
}

// checkTree compares the whole tree by size, one way. It returns how many
// files were compared, how many were missing or different, and the paths that
// matched (the sample is drawn from those, so a file that already failed is
// not reported a second time).
func (r *Runner) checkTree(ctx context.Context, src, dst string, env []string) (checked, bad int, matched []string) {
	var combined strings.Builder
	args := []string{
		"check", src, dst,
		"--one-way",   // extra files in Nextcloud are not a failure
		"--size-only", // no shared hash between Drive and Nextcloud
		"--combined", "-",
		"--checkers", "8",
	}
	// rclone exits non-zero when it finds differences; that is data, not a
	// failure of the check, so the exit code is ignored and the report parsed.
	_ = r.exec.Run(ctx, "rclone", args, env, func(line string) {
		combined.WriteString(line)
		combined.WriteByte('\n')
	})
	checked, bad, matched = parseCombined(combined.String())
	return checked, bad, matched
}

// checkSample picks a few files whose size already matched and compares them
// byte for byte by downloading both sides. A file the size pass already found
// bad is not re-checked: it would only report the same mismatch twice. What is
// left is exactly where a byte comparison adds information the size check
// could not see.
func (r *Runner) checkSample(ctx context.Context, src, dst string, env []string, matched []string) (checked, mismatch int, err error) {
	if len(matched) == 0 {
		return 0, 0, nil
	}
	picked := pick(matched, min(SampleSize, len(matched)))

	// rclone reads --files-from from a path, and the executor has no stdin, so
	// the list goes to a temp file that is removed whatever happens.
	list, err := os.CreateTemp("", "zlatan-sample-*.txt")
	if err != nil {
		return 0, 0, err
	}
	path := list.Name()
	defer os.Remove(path)
	if _, err := list.WriteString(strings.Join(picked, "\n") + "\n"); err != nil {
		list.Close()
		return 0, 0, err
	}
	if err := list.Close(); err != nil {
		return 0, 0, err
	}

	// One check call for the whole sample, downloaded: --download forces the
	// byte comparison rather than the (absent) shared hash.
	args := []string{
		"check", src, dst,
		"--one-way",
		"--download",
		"--files-from", path,
		"--combined", "-",
	}
	var combined strings.Builder
	_ = r.exec.Run(ctx, "rclone", args, env, func(line string) {
		combined.WriteString(line)
		combined.WriteByte('\n')
	})
	c, m, _ := parseCombined(combined.String())
	return c, m, nil
}

// parseImmichReport reads immich-go's end-of-run "Asset Tracking Report".
// immich-go verifies each asset's content hash against the server as it
// uploads — that is how it recognises a duplicate it already has — so
// "Processed" is a real check, not a claim. "Errors" and "Pending" are what
// would make the import incomplete.
//
// The report looks like:
//
//	Asset Tracking Report:
//	=====================
//	Total Assets:       1234  (5.6 GiB)
//	  Processed:        1230  (5.5 GiB)
//	  Discarded:           4  (0.1 GiB)
//	  Errors:              0  (0 B)
//	  Pending:             0  (0 B)
//
// Discarded is not a failure: those are duplicates and banned files, which
// immich-go is right to skip. Errors and Pending are.
func parseImmichReport(report string) (processed, discarded, errors, pending int) {
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		label := strings.TrimSuffix(fields[0], ":")
		n, ok := firstInt(fields[1:])
		if !ok {
			continue
		}
		switch label {
		case "Processed":
			processed = n
		case "Discarded":
			discarded = n
		case "Errors":
			errors = n
		case "Pending":
			pending = n
		}
	}
	return processed, discarded, errors, pending
}

// firstInt returns the first field that parses as a plain integer. The count
// comes before the human size ("1230  (5.5 GiB)"), so the first integer is the
// count.
func firstInt(fields []string) (int, bool) {
	for _, f := range fields {
		if n, err := strconv.Atoi(strings.Trim(f, "(),")); err == nil {
			return n, true
		}
	}
	return 0, false
}

// parseCombined reads rclone's --combined report. Each line is "<symbol>
// <path>": '=' matched, '*' differ, '+' missing on the destination, '-' extra
// on the destination, '!' an error. '=', '*', '+' and '!' count as compared;
// '*', '+' and '!' are failures. '-' is not: the destination may legitimately
// hold more than the source.
func parseCombined(report string) (checked, bad int, matched []string) {
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 2 {
			continue
		}
		symbol := line[0]
		path := strings.TrimSpace(line[1:])
		switch symbol {
		case '=', '*', '+', '!':
			checked++
		}
		switch symbol {
		case '*', '+', '!':
			bad++
		case '=':
			matched = append(matched, path)
		}
	}
	return checked, bad, matched
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// pick chooses n distinct entries at random, so the sample is not always the
// same files and a re-check looks somewhere new.
func pick(files []string, n int) []string {
	if n >= len(files) {
		return append([]string(nil), files...)
	}
	idx := rand.Perm(len(files))[:n]
	out := make([]string, 0, n)
	for _, i := range idx {
		out = append(out, files[i])
	}
	return out
}
