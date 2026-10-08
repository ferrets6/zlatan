// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"encoding/json"
	"strings"
)

// Progress is the line a track shows while it works. It is a catalogue key
// plus the numbers that fill it, never a finished sentence: the runner has no
// language, and the person reading the wizard does, so the sentence is chosen
// when the page is rendered.
//
// A progress with no arguments is stored as the bare key, which is the common
// case. One with arguments is stored as JSON, so a byte count travels as a
// number and the web layer formats it with the reader's decimal mark rather
// than the server's.
type Progress struct {
	Key  string  `json:"key"`
	Args []int64 `json:"args,omitempty"`
}

// The progress keys. Each is a phrase the wizard renders in the reader's
// language; the runner only ever writes the key.
const (
	ProgressPreparing       = "progress.preparing"
	ProgressCopying         = "progress.copying"
	ProgressChecking        = "progress.checking"
	ProgressInterrupted     = "progress.interrupted"
	ProgressNextcloudReady  = "progress.nextcloudReady"
	ProgressGoogleReady     = "progress.googleReady"
	ProgressConsentPending  = "progress.consentPending"
	ProgressAwaitingUpload  = "progress.awaitingUpload"
	ProgressAwaitingTakeout = "progress.awaitingTakeout"
	ProgressDownloading     = "progress.downloading"
	ProgressImporting       = "progress.importing"
	ProgressDriveVerified   = "progress.driveVerified"
	ProgressPhotosDone      = "progress.photosDone"
	// ProgressPhotosDoneDrive is done by the "Add to Drive" route, whose export
	// is still in the person's Drive for them to delete.
	ProgressPhotosDoneDrive = "progress.photosDoneDrive"
	// ProgressPhotosDoneAccepted is done by the person's choice: the import
	// left some files out and they accepted it as it is.
	ProgressPhotosDoneAccepted = "progress.photosDoneAccepted"
)

// The reconnect keys. They are reasons rather than progress, so they are also
// stored in last_error; using one key for both slots keeps a single phrase in
// the catalogue instead of two copies that can drift.
const (
	ReconnectGoogleCopy    = "reconnect.googleCopy"
	ReconnectNextcloudCopy = "reconnect.nextcloudCopy"
	ReconnectBothCopy      = "reconnect.bothCopy"
	ReconnectGoogleExport  = "reconnect.googleExport"
	ReconnectImmichImport  = "reconnect.immichImport"
	ReconnectImmichExport  = "reconnect.immichExport"
)

// The failure keys. A track that stops stores one of these in last_error, so
// the reason is rendered in the reader's language instead of being matched
// against a sentence the runner happened to write.
const (
	FailNextcloudMissing  = "why.nextcloudMissing"
	FailCopyPrepare       = "why.copyPrepare"
	FailCopyUnfinished    = "why.copyUnfinished"
	FailCopyUnchecked     = "why.copyUnchecked"
	FailMismatch          = "why.mismatch"
	FailTakeoutLate       = "why.takeoutLate"
	FailStagingPrepare    = "why.stagingPrepare"
	FailStagingRead       = "why.stagingRead"
	FailDownloadFailed    = "why.downloadFailed"
	FailNoArchive         = "why.noArchive"
	FailArchiveEmpty      = "why.archiveEmpty"
	FailArchiveUnreadable = "why.archiveUnreadable"
	FailImmichMissing     = "why.immichMissing"
	FailImportUnfinished  = "why.importUnfinished"
	FailImportErrors      = "why.importErrors"
	FailCredentialUnread  = "why.credentialUnreadable"
)

// ImportErrors is the reason an import that left files out stops on: how many
// errors and pending files, and, as a third argument, whether the archive came
// from the person's Drive (see FromDrive).
func ImportErrors(errs, pending int, fromDrive bool) Progress {
	return Progress{Key: FailImportErrors, Args: []int64{int64(errs), int64(pending), flag(fromDrive)}}
}

// PhotosDoneAccepted is the done an acceptance of files left out leaves. Its
// one argument carries the Drive route on from the failure it settles.
func PhotosDoneAccepted(fromDrive bool) Progress {
	return Progress{Key: ProgressPhotosDoneAccepted, Args: []int64{flag(fromDrive)}}
}

// FromDrive reports whether a Photos progress says its archive came from the
// person's Drive, where the export still takes up their Google storage. It is
// the one reader of the flag ImportErrors and PhotosDoneAccepted write.
func (p Progress) FromDrive() bool {
	switch p.Key {
	case ProgressPhotosDoneDrive:
		return true
	case FailImportErrors:
		return len(p.Args) > 2 && p.Args[2] == 1
	case ProgressPhotosDoneAccepted:
		return len(p.Args) > 0 && p.Args[0] == 1
	}
	return false
}

func flag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// EncodeProgress renders a progress for storage. A key with no arguments is
// stored as the key itself; arguments make it JSON.
func EncodeProgress(p Progress) string {
	if len(p.Args) == 0 {
		return p.Key
	}
	b, err := json.Marshal(p)
	if err != nil {
		// The only way this fails is a value json cannot represent, which the
		// int64 arguments cannot. Falling back to the key loses the numbers
		// but never the line.
		return p.Key
	}
	return string(b)
}

// DecodeProgress reads a stored progress line. A value that is not JSON is
// taken as a bare key, which also keeps a line written before the keys existed
// visible instead of blanking it.
func DecodeProgress(s string) Progress {
	if s == "" {
		return Progress{}
	}
	if !strings.HasPrefix(s, "{") {
		return Progress{Key: s}
	}
	var p Progress
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Progress{Key: s}
	}
	return p
}
