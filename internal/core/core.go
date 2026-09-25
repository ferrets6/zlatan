// SPDX-License-Identifier: AGPL-3.0-or-later

// Package core holds the domain types shared by every other package. It
// imports nothing from this module, so the dependency graph stays a tree.
package core

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Secret is a string that must never be printed. Formatting one yields a
// placeholder, so a stray log statement cannot leak a token (NFR-14).
type Secret string

// Reveal returns the underlying value. Call it only where the secret is
// actually used.
func (s Secret) Reveal() string { return string(s) }

// Empty reports whether no value is set.
func (s Secret) Empty() bool { return s == "" }

// String implements fmt.Stringer with a redaction.
func (s Secret) String() string {
	if s.Empty() {
		return "<unset>"
	}
	return "<redacted>"
}

// MarshalJSON redacts the value, so a struct containing a Secret can be
// encoded safely.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// Track is the two independent halves of a migration. Drive and Photos do not
// depend on each other: a person may migrate only one, or both in parallel.
type Track string

const (
	TrackDrive  Track = "drive"
	TrackPhotos Track = "photos"
)

// DriveState is the lifecycle of the Drive half.
type DriveState string

const (
	DriveNotStarted DriveState = "not_started"
	// DriveConsentPending covers both authorisations the Drive half needs: the
	// Google OAuth consent and the Nextcloud Login Flow grant. They are two
	// steps but one waiting state, because from the person's point of view they
	// are both "grant access in the browser and come back".
	DriveConsentPending DriveState = "consent_pending"
	DriveSelecting      DriveState = "selecting"
	DriveCopying        DriveState = "copying"
	DriveImporting      DriveState = "importing"
	DriveVerifying      DriveState = "verifying"
	DriveDone           DriveState = "done"
	DriveFailed         DriveState = "failed"
	DriveCancelled      DriveState = "cancelled"
)

// PhotosState is the lifecycle of the Photos half. The first human step is
// unavoidable: Google removed the Photos read scopes on 2025-03-31, so the
// only complete route is a Takeout the person requests themselves.
type PhotosState string

const (
	PhotosNotStarted     PhotosState = "not_started"
	PhotosTakeoutGuide   PhotosState = "takeout_guide"
	PhotosAwaitingShare  PhotosState = "awaiting_share"
	PhotosAwaitingUpload PhotosState = "awaiting_upload"
	PhotosDownloading    PhotosState = "downloading"
	PhotosImporting      PhotosState = "importing"
	PhotosVerifying      PhotosState = "verifying"
	PhotosDone           PhotosState = "done"
	PhotosFailed         PhotosState = "failed"
	PhotosCancelled      PhotosState = "cancelled"
)

// Terminal reports whether the state is an end state for its track.
func (s DriveState) Terminal() bool {
	return s == DriveDone || s == DriveFailed || s == DriveCancelled
}

// Terminal reports whether the state is an end state for its track.
func (s PhotosState) Terminal() bool {
	return s == PhotosDone || s == PhotosFailed || s == PhotosCancelled
}

// Migration is one person's progress across both tracks.
type Migration struct {
	User  string // the forward-auth identity, never a URL parameter
	Email string

	DriveState  DriveState
	PhotosState PhotosState

	// Progress is the last line a long-running step reported, shown to the
	// person so a multi-hour copy is not a blank page.
	DriveProgress  string
	PhotosProgress string

	// Counts are filled as steps complete, so the closing page can state
	// what actually happened rather than "done".
	DriveBytesCopied  int64
	DriveFilesCopied  int64
	PhotosAssetsAdded int64

	LastError string
	UpdatedAt time.Time
	CreatedAt time.Time
}

// State returns the state for a track.
func (m Migration) State(t Track) string {
	if t == TrackDrive {
		return string(m.DriveState)
	}
	return string(m.PhotosState)
}

// TrackSummary is what the wizard shows for one track.
type TrackSummary struct {
	Track    Track
	State    string
	Progress string
	Done     bool
	Failed   bool
}

// Summaries returns both tracks, ready for the template.
func (m Migration) Summaries() []TrackSummary {
	return []TrackSummary{
		{
			Track:    TrackDrive,
			State:    string(m.DriveState),
			Progress: m.DriveProgress,
			Done:     m.DriveState == DriveDone,
			Failed:   m.DriveState == DriveFailed,
		},
		{
			Track:    TrackPhotos,
			State:    string(m.PhotosState),
			Progress: m.PhotosProgress,
			Done:     m.PhotosState == PhotosDone,
			Failed:   m.PhotosState == PhotosFailed,
		},
	}
}

// Token is a stored OAuth refresh token for one person and one provider. The
// sealed value is ciphertext; the plaintext never touches the database.
type Token struct {
	User      string
	Provider  string
	Sealed    []byte
	Scopes    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Verify is the outcome of comparing a migrated sample against its source.
type Verify struct {
	User      string
	Track     Track
	Checked   int
	Matched   int
	Mismatch  int
	Detail    string
	CheckedAt time.Time
}

// OK reports whether the verification found nothing wrong.
func (v Verify) OK() bool { return v.Checked > 0 && v.Mismatch == 0 }

// RedactURL removes the userinfo from a URL, for logging. A URL with an
// embedded credential must never reach a log line. It parses rather than
// pattern-matches, so a path containing an '@' is not mistaken for userinfo.
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable: the safe move is not to echo it back.
		return "<unparseable url>"
	}
	if u.User == nil {
		return raw
	}
	// url.User percent-encodes the placeholder, so build the string by hand:
	// scheme://***@host/path. Dropping the userinfo keeps the host and path
	// readable, which is the point of logging it.
	u.User = nil
	withoutUser := u.String()
	if i := strings.Index(withoutUser, "://"); i >= 0 {
		return withoutUser[:i+3] + "***@" + withoutUser[i+3:]
	}
	return "***@" + withoutUser
}

// SafeName turns an untrusted string into a safe path element. It is the one
// sanitizer the whole service uses, so the runner and the upload store always
// agree on where a person's staging directory is: two implementations that
// drift apart would silently stop finding each other's files.
//
// Only letters, digits, dash and underscore survive. Dots are replaced, so no
// value can be "." or ".." or contain a path separator.
func SafeName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}

// FormatBytes renders a byte count for the wizard, in the units a person
// thinks in.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
