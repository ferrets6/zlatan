// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"embed"
	"html/template"
	"io/fs"
	"strconv"
	"strings"

	"github.com/marcodellemarche/zlatan/internal/core"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// staticRoot is staticFS rooted at the embedded directory, so the files are
// served at /static/style.css rather than /static/static/style.css. The embed
// directive keeps the directory prefix; StripPrefix in server.go removes it.
var staticRoot = func() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}()

// wizardTemplate renders the wizard. The helper set is deliberately small: the
// template formats a few values and does no logic, because logic belongs in Go
// where it can be tested.
var wizardTemplate = template.Must(
	template.New("wizard.html").Funcs(template.FuncMap{
		"bytes":     core.FormatBytes,
		"count":     count,
		"stateLine": stateLine,
		"pillClass": pillClass,
		"pillLabel": pillLabel,
	}).ParseFS(templateFS, "templates/wizard.html"),
)

// count groups a number with commas, so a nine-digit file count stays
// readable in the live region.
func count(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// stateLine is the calm sentence the live region leads with, one per state.
// It is the design's own copy, not the runner's technical progress: that is
// shown underneath as the "now doing" detail, where a file name or a byte
// count belongs.
func stateLine(track, state string) string {
	if track == string(core.TrackDrive) {
		switch core.DriveState(state) {
		case core.DriveConsentPending:
			return "Waiting for your consent in the browser"
		case core.DriveSelecting:
			return "Choosing what to copy"
		case core.DriveCopying:
			return "Copying your files into Nextcloud"
		case core.DriveImporting:
			return "Importing into Nextcloud"
		case core.DriveVerifying:
			return "Checking the copy matches"
		case core.DriveDone:
			return "Everything arrived"
		case core.DriveFailed, core.DriveCancelled:
			return "This track stopped"
		}
		return "Not started yet"
	}
	switch core.PhotosState(state) {
	case core.PhotosTakeoutGuide:
		return "Asking Google for a copy"
	case core.PhotosAwaitingShare:
		return "You have done your part. Google is preparing the copy."
	case core.PhotosAwaitingUpload:
		return "Waiting for your Takeout archive"
	case core.PhotosDownloading:
		return "Downloading the Takeout"
	case core.PhotosImporting:
		return "Unpacking your export and filling Immich"
	case core.PhotosVerifying:
		return "Checking the result"
	case core.PhotosDone:
		return "Everything arrived"
	case core.PhotosFailed, core.PhotosCancelled:
		return "This track stopped"
	}
	return "Not started yet"
}

// pillClass maps a track state onto the pill modifier the design uses. The
// names are the states in internal/core, so there is no translation table.
func pillClass(state string) string {
	switch state {
	case "not_started":
		return "pill--idle"
	case "done":
		return "pill--done"
	case "failed", "cancelled":
		return "pill--stopped"
	case "takeout_guide", "awaiting_upload":
		return "pill--you"
	case "consent_pending", "awaiting_nextcloud", "awaiting_share":
		return "pill--waiting"
	default:
		return "pill--running"
	}
}

// pillLabel is the short word in the pill. It is deliberately shorter than
// stateLine, which carries the full sentence for the live region.
func pillLabel(state string) string {
	switch state {
	case "not_started":
		return "Not started"
	case "consent_pending":
		return "Waiting for you"
	case "awaiting_nextcloud":
		return "Waiting for your consent"
	case "selecting":
		return "Choosing"
	case "copying":
		return "Copying"
	case "importing":
		return "Importing"
	case "verifying":
		return "Checking"
	case "takeout_guide":
		return "Your turn"
	case "awaiting_share":
		return "Waiting for the folder"
	case "awaiting_upload":
		return "Your turn"
	case "downloading":
		return "Downloading"
	case "done":
		return "Done"
	case "failed":
		return "Stopped"
	case "cancelled":
		return "Cancelled"
	}
	return state
}

// screenFor picks which of the design's screens the wizard renders. One page
// is always the truth; this decides which one.
//
// A failure anywhere wins, because the stopped screen shows both tracks and
// says the other one is fine. A finished pair wins next. After that, an active
// Drive track keeps the two-track view: the person asked for files too, and
// hiding that behind a Photos guide would lose it. Only when Drive is not
// running does the Photos half get its own single-track screen.
func screenFor(m core.Migration) string {
	if m.DriveState == core.DriveFailed || m.PhotosState == core.PhotosFailed {
		return "error"
	}
	if m.DriveState == core.DriveDone && m.PhotosState == core.PhotosDone {
		return "done"
	}
	if driveActive(m.DriveState) {
		return "tracks"
	}
	switch m.PhotosState {
	case core.PhotosTakeoutGuide:
		return "takeout"
	case core.PhotosAwaitingShare:
		return "share"
	case core.PhotosAwaitingUpload:
		return "upload"
	case core.PhotosDownloading, core.PhotosImporting, core.PhotosVerifying:
		return "waiting"
	}
	if m.DriveState == core.DriveNotStarted && m.PhotosState == core.PhotosNotStarted {
		return "entry"
	}
	return "tracks"
}

// driveActive reports whether the Drive half has work in flight.
func driveActive(s core.DriveState) bool {
	switch s {
	case core.DriveNotStarted, core.DriveDone, core.DriveFailed, core.DriveCancelled:
		return false
	}
	return true
}
