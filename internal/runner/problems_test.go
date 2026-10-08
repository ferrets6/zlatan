// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// stoppedOnProblems puts the fake store where an import that left files out
// leaves it: Photos failed on that reason, with the files in the verification.
func stoppedOnProblems(s *fakeStore, problems ...core.Problem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migration.PhotosState = core.PhotosFailed
	s.migration.PhotosProgress = core.EncodeProgress(core.ImportErrors(0, len(problems), false))
	s.migration.PhotosAssetsAdded = 11383
	s.verification = core.Verify{Track: core.TrackPhotos, Checked: 11384, Matched: 11384 - len(problems),
		Mismatch: len(problems), Problems: problems, Detail: "immich-go processed 11383 assets"}
}

// Accepting is open only while Photos is stopped on files left out, and then
// ends the half as done, by the person's choice.
func TestAcceptPhotos(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	store.migration.PhotosState = core.PhotosFailed
	store.migration.PhotosProgress = core.EncodeProgress(core.Progress{Key: core.FailImmichMissing})
	if err := r.AcceptPhotos(ctx, "marco"); !errors.Is(err, core.ErrNoProblemsToSettle) {
		t.Fatalf("AcceptPhotos on another reason = %v, want ErrNoProblemsToSettle", err)
	}

	stoppedOnProblems(store, core.Problem{File: "takeout-1:Takeout/a(1).MP4", Pending: true, Reason: "missing metadata"})
	if err := r.AcceptPhotos(ctx, "marco"); err != nil {
		t.Fatalf("AcceptPhotos: %v", err)
	}
	m := store.state()
	if m.PhotosState != core.PhotosDone || core.DecodeProgress(m.PhotosProgress).Key != core.ProgressPhotosDoneAccepted {
		t.Errorf("state = %q / %q, want done by acceptance", m.PhotosState, m.PhotosProgress)
	}
}

// The retry copies only the files left out into a folder of their own and
// imports that folder with immich-go's folder mode. What it imports adds to
// the count instead of replacing it, and the folder is gone afterwards.
func TestRunProblemsRetrySendsOnlyThoseFiles(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{lines: []string{
		"Asset Tracking Report:",
		"  Processed:         1  (7.3 MB)",
		"  Discarded:         0  (0 B)",
		"  Errors:            0  (0 B)",
		"  Pending:           0  (0 B)",
	}}
	var folder string
	var sent []string
	var newest time.Time
	exec.onRun = func(args []string) {
		if !slices.Contains(args, "from-folder") {
			t.Errorf("the retry should use the folder mode, args %v", args)
		}
		folder = args[len(args)-1]
		_ = filepath.WalkDir(folder, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(folder, path)
				sent = append(sent, filepath.ToSlash(rel))
				if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			return nil
		})
	}
	r := newRunner(t, store, exec)
	seedImmich(t, store, sealerOf(t, r))
	staging := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))
	writeTakeout(t, staging, "takeout-1.zip", "takeout-2.zip")

	problems := []core.Problem{
		{File: "takeout-2:Takeout/Google Photos/photo-1.jpg", Pending: true, Reason: "missing metadata"},
		{File: "takeout-9:Takeout/gone.jpg", Reason: "upload error"},
	}
	stoppedOnProblems(store, problems...)
	r.runProblemsRetry(context.Background(), "marco", settle{prev: store.verification})

	if want := []string{"0/photo-1.jpg"}; !slices.Equal(sent, want) {
		t.Errorf("sent %v, want %v: only the file found in its archive", sent, want)
	}
	// The test archive's entries carry no date, which zip stores as 1980: the
	// copy must keep it, not take the time of the copy, or folder mode would
	// date a photo with no date inside it as taken today.
	if newest.After(time.Now().Add(-24 * time.Hour)) {
		t.Errorf("an extracted file is dated %v: the zip's date should be kept", newest)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the retry folder should be removed, stat = %v", err)
	}
	store.mu.Lock()
	assets, v := store.photos, store.verification
	store.mu.Unlock()
	if assets != 11384 {
		t.Errorf("assets = %d, want 11383 + 1", assets)
	}
	// The file not found in the archives is still left out, so the track is
	// not done, and the record still describes the whole library.
	if got := store.state().PhotosState; got != core.PhotosFailed {
		t.Errorf("photos state = %q, want failed: one file was never sent", got)
	}
	if v.Checked != 11384 || v.Mismatch != 1 || len(v.Problems) != 1 || v.Problems[0].File != "takeout-9:Takeout/gone.jpg" {
		t.Errorf("verification = %+v, want the library's counts with the missing file left out", v)
	}
}

// The retry claims the track at once, before waiting for the heavy slot: a
// second click, or an "accept" from a stale tab, then finds nothing to settle.
// A list cut at MaxProblems is not retried at all: calling the track done
// after a hundred of five hundred would drop the rest without a word.
func TestRetryPhotosProblemsClaimsAndNeedsTheWholeList(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedImmich(t, store, sealerOf(t, r))

	stoppedOnProblems(store, core.Problem{File: "takeout-1:Takeout/a.jpg", Pending: true})
	store.mu.Lock()
	store.verification.Mismatch = 500
	store.mu.Unlock()
	if err := r.RetryPhotosProblems(ctx, "marco"); !errors.Is(err, core.ErrProblemsIncomplete) {
		t.Fatalf("retry of a partial list = %v, want ErrProblemsIncomplete", err)
	}

	stoppedOnProblems(store, core.Problem{File: "takeout-1:Takeout/a.jpg", Pending: true})
	// Hold the heavy slot, so the retry stays queued behind it for the rest of
	// the test, as it would behind another person's import.
	r.acquire()
	if err := r.RetryPhotosProblems(ctx, "marco"); err != nil {
		t.Fatalf("RetryPhotosProblems: %v", err)
	}
	if got := store.state().PhotosState; got != core.PhotosImporting {
		t.Fatalf("photos state = %q, want importing at once", got)
	}
	if err := r.RetryPhotosProblems(ctx, "marco"); !errors.Is(err, core.ErrNoProblemsToSettle) {
		t.Errorf("a second retry = %v, want ErrNoProblemsToSettle", err)
	}
	if err := r.AcceptPhotos(ctx, "marco"); !errors.Is(err, core.ErrNoProblemsToSettle) {
		t.Errorf("accept while the retry is queued = %v, want ErrNoProblemsToSettle", err)
	}
}

// Whether the archive came from the person's Drive survives the failure, so an
// acceptance still leaves the done that reminds them to delete the export.
func TestAcceptKeepsTheDriveRoute(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	stoppedOnProblems(store, core.Problem{File: "takeout-1:Takeout/a.jpg", Pending: true})
	store.mu.Lock()
	store.migration.PhotosProgress = core.EncodeProgress(core.ImportErrors(0, 1, true))
	store.mu.Unlock()

	if err := r.AcceptPhotos(context.Background(), "marco"); err != nil {
		t.Fatalf("AcceptPhotos: %v", err)
	}
	done := core.DecodeProgress(store.state().PhotosProgress)
	if done.Key != core.ProgressPhotosDoneAccepted || !slices.Equal(done.Args, []int64{1}) {
		t.Errorf("done = %+v, want the accepted done carrying the Drive route", done)
	}
}

// A retry that leaves a file out again names it by its place in the archives,
// so the next "retry only these" can still find it.
func TestOriginOf(t *testing.T) {
	origin := []core.Problem{{File: "takeout-2:Takeout/Foto da 2023/a(1).MP4"}}
	for _, name := range []string{
		"/staging/marco/.retry-123/0/a(1).MP4",
		".retry-123:0/a(1).MP4",
	} {
		if got := originOf(name, origin); got != origin[0].File {
			t.Errorf("originOf(%q) = %q, want %q", name, got, origin[0].File)
		}
	}
	if got := originOf("takeout-2:Takeout/b.jpg", origin); got != "takeout-2:Takeout/b.jpg" {
		t.Errorf("a name that is not the retry's should stay as it is, got %q", got)
	}
}

// "Retry the import" claims the track at once, like the retry of the files
// left out: queued behind another import it already shows importing, so the
// same card's other buttons find it taken. A track that is not stopped is
// refused before anything else runs.
func TestStartPhotosImportClaimsTheStoppedTrack(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedImmich(t, store, sealerOf(t, r))
	stoppedOnProblems(store, core.Problem{File: "takeout-1:Takeout/a.jpg", Pending: true})

	r.acquire() // another import holds the heavy slot for the rest of the test
	if err := r.StartPhotosImport(ctx, "marco"); err != nil {
		t.Fatalf("StartPhotosImport: %v", err)
	}
	if got := store.state().PhotosState; got != core.PhotosImporting {
		t.Fatalf("photos state = %q, want importing at once", got)
	}
	if err := r.StartPhotosImport(ctx, "marco"); !errors.Is(err, core.ErrPhotosNotStopped) {
		t.Errorf("a second click = %v, want ErrPhotosNotStopped", err)
	}
	if err := r.AcceptPhotos(ctx, "marco"); !errors.Is(err, core.ErrNoProblemsToSettle) {
		t.Errorf("accept while the import is queued = %v, want ErrNoProblemsToSettle", err)
	}
}

// A retry of a few files that all fail again keeps the track on the choices,
// rather than "the import did not finish".
func TestRetryThatFailsAgainKeepsTheList(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{
		lines: []string{
			"Asset Tracking Report:",
			"  Processed:         0  (0 B)",
			"  Discarded:         0  (0 B)",
			"  Errors:            1  (7.3 MB)",
			"  Pending:           0  (0 B)",
		},
		err: errors.New("exit status 1"),
	}
	r := newRunner(t, store, exec)
	seedImmich(t, store, sealerOf(t, r))
	writeTakeout(t, filepath.Join(r.cfg.StagingDir, core.SafeName("marco")))
	problems := []core.Problem{{File: "takeout-1:Takeout/Google Photos/photo-0.jpg", Pending: true, Reason: "missing metadata"}}
	stoppedOnProblems(store, problems...)

	r.runProblemsRetry(context.Background(), "marco", settle{prev: store.verification})

	m := store.state()
	if m.PhotosState != core.PhotosFailed || core.DecodeProgress(m.PhotosProgress).Key != core.FailImportErrors {
		t.Errorf("state = %q / %q, want stopped on the files left out", m.PhotosState, m.PhotosProgress)
	}
	store.mu.Lock()
	v := store.verification
	store.mu.Unlock()
	// The fake immich-go writes no log, so no name: the card then says the log
	// has them and still offers to accept, rather than guess which of the
	// files sent it was. The counts stay the library's.
	if v.Checked != 11384 || v.Mismatch != 1 || len(v.Problems) != 0 {
		t.Errorf("verification = %+v, want the library's counts and no guessed names", v)
	}
	if !m.LeftOut() {
		t.Error("the track should stay where the files can be accepted")
	}
}

// A retry that finds none of the files in the archives puts the track back on
// the files left out, now marked not found: accept and the full retry stay on
// the card, and a retry of these, which would find nothing again, is not.
func TestRetryThatFindsNothingRestoresTheChoices(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedImmich(t, store, sealerOf(t, r))
	writeTakeout(t, filepath.Join(r.cfg.StagingDir, core.SafeName("marco")))
	problems := []core.Problem{{File: "takeout-9:Takeout/gone.jpg", Pending: true, Reason: "missing metadata"}}
	stoppedOnProblems(store, problems...)

	r.runProblemsRetry(context.Background(), "marco", settle{prev: store.verification})

	if m := store.state(); !m.LeftOut() {
		t.Errorf("state = %q / %q, want back on the files left out", m.PhotosState, m.PhotosProgress)
	}
	store.mu.Lock()
	v := store.verification
	store.mu.Unlock()
	want := problems[0]
	want.Missing = true
	if !slices.Equal(v.Problems, []core.Problem{want}) || v.CanRetryProblems() {
		t.Errorf("problems = %+v, want the same file marked not found, with no retry of it", v.Problems)
	}
}

// An import whose log named none of what it left out can still be accepted.
func TestAcceptWithoutNames(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	stoppedOnProblems(store)
	store.mu.Lock()
	store.verification.Mismatch = 1
	store.mu.Unlock()
	if err := r.AcceptPhotos(context.Background(), "marco"); err != nil {
		t.Fatalf("AcceptPhotos with no names: %v", err)
	}
	if err := r.RetryPhotosProblems(context.Background(), "marco"); !errors.Is(err, core.ErrNoProblemsToSettle) {
		t.Errorf("retry with no names = %v, want ErrNoProblemsToSettle", err)
	}
}

// A retry whose report accounts for fewer files than were sent (folder mode
// skipped one without a word) is not done: the rest are still left out. Its
// detail says what this retry did, once, from the import's own detail.
func TestRetryWithFilesUnaccountedForIsNotDone(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{lines: []string{
		"Asset Tracking Report:",
		"  Processed:         1  (7.3 MB)",
		"  Discarded:         0  (0 B)",
		"  Errors:            0  (0 B)",
		"  Pending:           0  (0 B)",
	}}
	r := newRunner(t, store, exec)
	seedImmich(t, store, sealerOf(t, r))
	writeTakeout(t, filepath.Join(r.cfg.StagingDir, core.SafeName("marco")), "takeout-1.zip", "takeout-2.zip")
	problems := []core.Problem{
		{File: "takeout-1:Takeout/Google Photos/photo-0.jpg", Pending: true},
		{File: "takeout-2:Takeout/Google Photos/photo-1.jpg", Pending: true},
	}
	stoppedOnProblems(store, problems...)
	store.mu.Lock()
	store.verification.Detail = "immich-go processed 11382 assets" + retryDetail + " 3 files left out: earlier"
	store.mu.Unlock()

	r.runProblemsRetry(context.Background(), "marco", settle{prev: store.verification})

	if m := store.state(); m.PhotosState != core.PhotosFailed {
		t.Errorf("photos state = %q, want failed: one file sent got no outcome", m.PhotosState)
	}
	store.mu.Lock()
	v := store.verification
	store.mu.Unlock()
	if v.Mismatch != 1 {
		t.Errorf("mismatch = %d, want the one file with no outcome", v.Mismatch)
	}
	if strings.Count(v.Detail, retryDetail) != 1 || !strings.HasPrefix(v.Detail, "immich-go processed 11382 assets"+retryDetail+" 2 files") {
		t.Errorf("detail = %q, want the import's detail and this retry's, once", v.Detail)
	}
}

// "Retry the import" carries the Drive route on from the run it retries.
func TestStartPhotosImportKeepsTheDriveRoute(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{lines: []string{
		"Asset Tracking Report:",
		"  Processed:        12  (1.2 GiB)",
		"  Discarded:         0  (0 B)",
		"  Errors:            0  (0 B)",
		"  Pending:           0  (0 B)",
	}}
	done := make(chan struct{})
	exec.onRun = func([]string) { close(done) }
	r := newRunner(t, store, exec)
	seedImmich(t, store, sealerOf(t, r))
	writeTakeout(t, filepath.Join(r.cfg.StagingDir, core.SafeName("marco")))
	stoppedOnProblems(store, core.Problem{File: "takeout-1:Takeout/a.jpg", Pending: true})
	store.mu.Lock()
	store.migration.PhotosProgress = core.EncodeProgress(core.ImportErrors(0, 1, true))
	store.mu.Unlock()

	if err := r.StartPhotosImport(context.Background(), "marco"); err != nil {
		t.Fatalf("StartPhotosImport: %v", err)
	}
	<-done
	for i := 0; i < 200 && store.state().PhotosState != core.PhotosDone; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := core.DecodeProgress(store.state().PhotosProgress); got.Key != core.ProgressPhotosDoneDrive {
		t.Errorf("done = %+v, want the Drive route's done", got)
	}
}

// A stale tab's "send the files" cannot pull a queued import out from under
// itself.
func TestBeginPhotosUploadLeavesARunningTrack(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedImmich(t, store, sealerOf(t, r))
	store.migration.PhotosState = core.PhotosImporting
	if err := r.BeginPhotosUpload(context.Background(), "marco"); !errors.Is(err, core.ErrPhotosNotStopped) {
		t.Errorf("BeginPhotosUpload while importing = %v, want ErrPhotosNotStopped", err)
	}
	if got := store.state().PhotosState; got != core.PhotosImporting {
		t.Errorf("photos state = %q, want still importing", got)
	}
}
