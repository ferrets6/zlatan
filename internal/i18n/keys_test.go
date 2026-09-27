package i18n

import (
	"testing"

	"github.com/marcodellemarche/zlatan/internal/core"
)

func TestEveryStoredKeyHasAPhrase(t *testing.T) {
	keys := []string{
		core.ProgressPreparing, core.ProgressCopying, core.ProgressChecking, core.ProgressInterrupted,
		core.ProgressNextcloudReady, core.ProgressGoogleReady, core.ProgressConsentPending,
		core.ProgressAwaitingUpload, core.ProgressAwaitingTakeout, core.ProgressDownloading,
		core.ProgressImporting, core.ProgressDriveVerified, core.ProgressPhotosDone,
		core.ReconnectGoogleCopy, core.ReconnectNextcloudCopy, core.ReconnectBothCopy,
		core.ReconnectGoogleExport, core.ReconnectImmichImport, core.ReconnectImmichExport,
		core.FailNextcloudMissing, core.FailCopyPrepare, core.FailCopyUnfinished, core.FailCopyUnchecked,
		core.FailMismatch, core.FailTakeoutLate, core.FailStagingPrepare, core.FailStagingRead,
		core.FailDownloadFailed, core.FailNoArchive, core.FailArchiveEmpty, core.FailArchiveUnreadable,
		core.FailImmichMissing, core.FailImportUnfinished, core.FailImportErrors, core.FailCredentialUnread,
	}
	for _, k := range keys {
		for _, l := range Supported {
			if got := T(l, k); got == k {
				t.Errorf("%s is missing %q", l, k)
			}
		}
	}
}
