// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// MaxParts bounds the declared count. Google splits at 50 GB at most, so a
// hundred parts is five terabytes of photos: far past anything real, and a
// typo like 300 is caught instead of waiting forever.
const MaxParts = 99

// uploadSweep is how often the server looks for auto-import migrations whose
// parts have all arrived. The upload route (site or kiosk) writes files the
// browser may not be watching, so a minute is a good balance.
const uploadSweep = time.Minute

// PhotosParts measures what is in the person's staging area against the count
// they declared. The expected count is passed in (the caller usually already
// holds the migration), so this does not read the row again.
func (r *Runner) PhotosParts(user string, expected int) (core.Parts, error) {
	names, err := r.stagedArchives(user)
	if err != nil {
		return core.Parts{}, err
	}
	return core.CountParts(names, expected), nil
}

// DeclarePhotosParts records how many archives the person says Google gave
// them. It does NOT touch the auto-import flag: that is its own setting
// (SetAutoImport, /photos/auto), so re-saving the count never flips it. If the
// files are already here (e.g. downloaded via kiosk before the number was
// entered) and auto-import is on, it starts straight away.
func (r *Runner) DeclarePhotosParts(ctx context.Context, user string, parts int) error {
	if parts < 1 || parts > MaxParts {
		return core.ErrPartsOutOfRange
	}
	m, err := r.store.GetMigration(ctx, user)
	if errors.Is(err, store.ErrNoMigration) {
		// No row yet means the person is not on the upload screen: the same
		// "not waiting for an upload" the state check reports, not a server fault.
		return core.ErrNotUploading
	}
	if err != nil {
		return err
	}
	if m.PhotosState != core.PhotosAwaitingUpload {
		return core.ErrNotUploading
	}
	if err := r.store.SetPhotosParts(ctx, user, parts); err != nil {
		return err
	}
	// Saving the count is the primary action and it has succeeded. Starting the
	// import now (if auto is on and the files are already here) is best effort:
	// a failure here must not report the save as failed. The sweep and the
	// explicit Start button surface a real start problem.
	if _, err := r.AutoImportIfEnabled(ctx, user); err != nil {
		r.log.Warn("DeclarePhotosParts: auto-start after declare", "user", user, "error", err)
	}
	return nil
}

// SetAutoImport toggles automatic start at any time — the person can change
// their mind during an upload or a kiosk download. Turning it on starts the
// import at once if every part is already here.
func (r *Runner) SetAutoImport(ctx context.Context, user string, on bool) error {
	if err := r.store.SetAutoImport(ctx, user, on); err != nil {
		return err
	}
	// Saving the setting is the primary action and it has succeeded. Starting
	// the import now (if the files are already here) is best effort, like
	// DeclarePhotosParts: a failure must not report the save as failed, and the
	// runner has already moved the track to where the person can act next.
	if on {
		if _, err := r.AutoImportIfEnabled(ctx, user); err != nil {
			r.log.Warn("SetAutoImport: auto-start after toggle", "user", user, "error", err)
		}
	}
	return nil
}

// AutoImportIfEnabled starts the import only when the person asked for an
// automatic start. It is the trigger the upload completion and the server-side
// sweep both use, so the browser never has to decide to start anything: the
// server does, and the browser only polls and shows the result.
func (r *Runner) AutoImportIfEnabled(ctx context.Context, user string) (bool, error) {
	m, err := r.store.GetMigration(ctx, user)
	if err != nil {
		return false, err
	}
	if !m.AutoImport {
		return false, nil
	}
	return r.startUploadedImport(ctx, user)
}

// StartImportIfComplete starts the import now if every declared part is on
// disk. It is the explicit "Start" button, so it ignores the auto-import flag;
// it still refuses to import an incomplete set.
func (r *Runner) StartImportIfComplete(ctx context.Context, user string) (bool, error) {
	return r.startUploadedImport(ctx, user)
}

// startUploadedImport is the shared core: under the claim lock, start the
// import if and only if the Photos track is waiting for an upload and every
// declared part is present. A Takeout split into several files must be imported
// in one go, because a photo's date and albums can sit in a JSON file in
// another part.
//
// The claim — "is it complete, and move it to importing" — is made under a
// lock, so two parts that finish at the same moment start one import, not two.
// launchPhotosImport runs OUTSIDE the lock, because it makes a network preflight
// to Immich: holding the process-wide lock across it would serialise every
// other user's upload behind one slow probe.
func (r *Runner) startUploadedImport(ctx context.Context, user string) (bool, error) {
	claimed, err := r.claimForImport(ctx, user)
	if err != nil || !claimed {
		return false, err
	}
	if err := r.launchPhotosImport(ctx, user, nil); err != nil {
		// Leave the track where the person can act next. This is the one place
		// that decides it, so the explicit Start button and the sweep behave the
		// same way.
		switch {
		case errors.Is(err, core.ErrCredentialRefused):
			// launchPhotosImport already moved it to the reconnect screen.
		case errors.Is(err, store.ErrNoToken):
			// The Immich key is gone: no retry brings it back. Fail the track so
			// the person gets a stopped screen with a retry.
			r.failPhotos(ctx, user, core.Progress{Key: core.FailImmichMissing})
		case errors.Is(err, core.ErrCredentialUnreadable):
			// The key is there but will not unseal (the token key changed, or the
			// row is corrupt). Keep it — restoring the key brings it back — but
			// fail the track so it is not stuck on a screen that cannot move.
			r.failPhotos(ctx, user, core.Progress{Key: core.FailCredentialUnread})
		default:
			// Transient: put it back on the upload screen, where the archives
			// still are and the next attempt (button or sweep) can start.
			if _, serr := r.store.SetPhotosState(ctx, user, core.PhotosAwaitingUpload,
				core.EncodeProgress(core.Progress{Key: core.ProgressAwaitingUpload})); serr != nil {
				r.log.Error("startUploadedImport: release the claim", "user", user, "error", serr)
			}
		}
		return false, err
	}
	return true, nil
}

// claimForImport moves the track to importing if and only if it is waiting for
// an upload and every declared part is present, all under the claim lock so the
// move is atomic against a concurrent part landing.
func (r *Runner) claimForImport(ctx context.Context, user string) (bool, error) {
	r.claim.Lock()
	defer r.claim.Unlock()

	m, err := r.store.GetMigration(ctx, user)
	if err != nil {
		return false, err
	}
	if m.PhotosState != core.PhotosAwaitingUpload {
		return false, nil
	}
	names, err := r.stagedArchives(user)
	if err != nil {
		return false, err
	}
	if !core.CountParts(names, m.PhotosPartsExpected).Complete() {
		return false, nil
	}
	// The import moves the state itself, but only once it holds the heavy slot,
	// which can be hours away; claiming here, atomically, is what stops a second
	// finishing part from starting it twice.
	if _, err := r.store.SetPhotosState(ctx, user, core.PhotosImporting,
		core.EncodeProgress(core.Progress{Key: core.ProgressImporting})); err != nil {
		return false, err
	}
	return true, nil
}

// WatchUploads starts the import for every auto-import migration whose parts
// have all arrived. It runs for the life of the process, one loop for everyone:
// the state is in the database, so a kiosk download started with the browser
// then closed is still imported once the last part lands. It does nothing for a
// migration whose auto-import is off — that one waits for an explicit "Start".
func (r *Runner) WatchUploads(ctx context.Context) {
	ticker := time.NewTicker(uploadSweep)
	defer ticker.Stop()

	r.log.Info("upload sweeper started", "interval", uploadSweep)
	r.sweepUploads(ctx)
	for {
		select {
		case <-ctx.Done():
			r.log.Info("upload sweeper stopped")
			return
		case <-ticker.C:
			r.sweepUploads(ctx)
		}
	}
}

func (r *Runner) sweepUploads(ctx context.Context) {
	users, err := r.store.ListAutoImportWaiting(ctx)
	if err != nil {
		r.log.Error("upload sweeper: list", "error", err)
		return
	}
	for _, user := range users {
		// startUploadedImport already decides what to do with the track on an
		// error (fail it on a permanent one, leave it waiting on a transient one),
		// so a permanent failure stops the sweep re-picking it next tick. Here we
		// only log.
		if _, err := r.AutoImportIfEnabled(ctx, user); err != nil {
			r.log.Warn("upload sweeper: start", "user", user, "error", err)
		}
	}
}

// stagedArchives lists the completed archives in the person's staging area, by
// name. Both the site upload and the kiosk browser write them there, and the
// import reads them there. A download still in progress (kiosk writes a
// .crdownload, the site a .parts directory) is not a .zip yet, so it is not
// counted.
func (r *Runner) stagedArchives(user string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(r.cfg.StagingDir, core.SafeName(user), "*.zip"))
	if err != nil {
		return nil, err
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return names, nil
}
