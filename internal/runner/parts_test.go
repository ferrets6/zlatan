// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/immich"
)

// An upload split over several files starts the import only when the last
// declared part lands, and starts it once: importing part 1 alone would lose
// the dates and albums whose JSON sits in part 2.
func TestImportUploadWaitsForEveryDeclaredPart(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	exec := &fakeExecutor{byCommand: map[string]scripted{
		"upload": {lines: []string{
			"Asset Tracking Report:",
			"  Processed:        2  (1.0 MiB)",
			"  Discarded:        0  (0 B)",
			"  Errors:           0  (0 B)",
			"  Pending:          0  (0 B)",
		}},
	}}
	r := newRunner(t, store, exec).WithImmich(&fakeImmich{me: immich.Me{Email: "marco@example.com"}})
	seedImmich(t, store, sealerOf(t, r))
	store.migration.PhotosState = core.PhotosAwaitingUpload
	staging := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))

	// The count is declared (without auto-start): nothing to start yet.
	if err := r.DeclarePhotosParts(ctx, "marco", 2); err != nil {
		t.Fatalf("DeclarePhotosParts: %v", err)
	}
	writeTakeout(t, staging, "takeout-x-001.zip")
	if started, err := r.StartImportIfComplete(ctx, "marco"); err != nil || started {
		t.Fatalf("one part of two: started = %v, err = %v", started, err)
	}
	if got := store.state().PhotosState; got != core.PhotosAwaitingUpload {
		t.Fatalf("photos state = %q, want awaiting_upload while a part is missing", got)
	}
	if parts, _ := r.PhotosParts("marco", 2); len(parts.Missing) != 1 || parts.Missing[0] != 2 {
		t.Errorf("Missing = %v, want [2]", parts.Missing)
	}

	writeTakeout(t, staging, "takeout-x-002.zip")
	if started, err := r.StartImportIfComplete(ctx, "marco"); err != nil || !started {
		t.Fatalf("both parts: started = %v, err = %v", started, err)
	}
	// A second file finishing at the same moment must not start a second one.
	if started, _ := r.StartImportIfComplete(ctx, "marco"); started {
		t.Fatal("the import was started twice")
	}

	waitFor(t, func() bool { return store.state().PhotosState == core.PhotosDone })
	if got := store.state().PhotosState; got != core.PhotosDone {
		t.Fatalf("photos state = %q, want done", got)
	}
	cmd, ok := exec.first("upload")
	if !ok {
		t.Fatal("immich-go never ran")
	}
	// Both parts go to immich-go in the same run.
	var archives int
	for _, a := range cmd.args {
		if filepath.Ext(a) == ".zip" {
			archives++
		}
	}
	if archives != 2 {
		t.Errorf("immich-go got %d archives, want both in one run: %v", archives, cmd.args)
	}
}

// Auto-import is the opt-in: off, a complete set does not start on its own and
// waits for the explicit button; on, the server-side sweep starts it — which is
// what lets a kiosk download finish with the zlatan tab closed.
func TestAutoImportIsOptIn(t *testing.T) {
	ctx := context.Background()
	report := &fakeExecutor{byCommand: map[string]scripted{
		"upload": {lines: []string{
			"Asset Tracking Report:",
			"  Processed:        1  (1.0 MiB)",
			"  Discarded:        0  (0 B)",
			"  Errors:           0  (0 B)",
			"  Pending:          0  (0 B)",
		}},
	}}
	store := newFakeStore()
	r := newRunner(t, store, report).WithImmich(&fakeImmich{me: immich.Me{Email: "marco@example.com"}})
	seedImmich(t, store, sealerOf(t, r))
	store.migration.PhotosState = core.PhotosAwaitingUpload
	staging := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))

	// One complete part, auto-import OFF: the sweep leaves it alone.
	if err := r.DeclarePhotosParts(ctx, "marco", 1); err != nil {
		t.Fatalf("DeclarePhotosParts: %v", err)
	}
	writeTakeout(t, staging, "takeout-1.zip")
	// The flag gates the automatic trigger directly (the upload-completion path),
	// and the sweep gates it through the store query (the kiosk path): both must
	// leave a complete-but-opted-out migration alone.
	if started, err := r.AutoImportIfEnabled(ctx, "marco"); err != nil || started {
		t.Fatalf("auto off: AutoImportIfEnabled started = %v, err = %v, want no start", started, err)
	}
	r.sweepUploads(ctx)
	if got := store.state().PhotosState; got != core.PhotosAwaitingUpload {
		t.Fatalf("auto off: state = %q, want awaiting_upload (no auto start)", got)
	}

	// Turn it on: SetAutoImport starts at once because the file is already here.
	if err := r.SetAutoImport(ctx, "marco", true); err != nil {
		t.Fatalf("SetAutoImport: %v", err)
	}
	waitFor(t, func() bool { return store.state().PhotosState == core.PhotosDone })
	if got := store.state().PhotosState; got != core.PhotosDone {
		t.Fatalf("auto on: state = %q, want done", got)
	}
}

// A key that is present but will not unseal (the token key changed, or the row
// is corrupt) must stop the sweep, not loop it every minute. The track fails
// with the unreadable reason and the key is kept, so restoring the key brings
// it back.
func TestSweepStopsOnAnUnreadableKey(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	store.tokens[immich.Provider] = core.Token{User: "marco", Provider: immich.Provider, Sealed: []byte("not-a-sealed-value")}
	store.migration.PhotosState = core.PhotosAwaitingUpload
	store.migration.AutoImport = true
	store.migration.PhotosPartsExpected = 1
	writeTakeout(t, filepath.Join(r.cfg.StagingDir, core.SafeName("marco")), "takeout-1.zip")

	r.sweepUploads(ctx)

	if got := store.state().PhotosState; got != core.PhotosFailed {
		t.Fatalf("state = %q, want failed: an unreadable key must stop the sweep", got)
	}
	if got := core.DecodeProgress(store.state().LastError).Key; got != core.FailCredentialUnread {
		t.Errorf("reason = %q, want %q", got, core.FailCredentialUnread)
	}
	if _, ok := store.tokens[immich.Provider]; !ok {
		t.Error("an unreadable key must be kept, not forgotten")
	}
}

func TestDeclarePhotosPartsRefusesWhatCannotBeRight(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	store.migration.PhotosState = core.PhotosAwaitingUpload
	for _, n := range []int{0, -1, MaxParts + 1} {
		if err := r.DeclarePhotosParts(ctx, "marco", n); !errors.Is(err, core.ErrPartsOutOfRange) {
			t.Errorf("DeclarePhotosParts(%d) = %v, want ErrPartsOutOfRange", n, err)
		}
	}

	// Outside the upload screen there is no count to declare.
	store.migration.PhotosState = core.PhotosImporting
	if err := r.DeclarePhotosParts(ctx, "marco", 2); !errors.Is(err, core.ErrNotUploading) {
		t.Errorf("DeclarePhotosParts while importing = %v, want ErrNotUploading", err)
	}
	if store.state().PhotosPartsExpected != 0 {
		t.Error("a refused count must not be stored")
	}
}
