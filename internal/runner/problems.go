// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// An import that leaves files out stops with their names (see immichProblems)
// and two ways forward besides retrying everything: send only those files
// again, or accept the result as it is. Both are open only while Photos is
// stopped on exactly that outcome, and both claim the track under the claim
// lock before doing anything, so a double click or a stale tab cannot run
// them twice or against each other.

// settle is what an import that left files out leaves behind.
type settle struct {
	// prev is that import's verification: its counts and the files it names.
	prev core.Verify
	// fromDrive: the archive came from the person's Drive (see photosRun).
	fromDrive bool
}

// settleable reads what the last import left out, or ErrNoProblemsToSettle
// when Photos did not stop on that (see core.Migration.LeftOut). An import
// whose log named no file can still be accepted: the list is a help to decide,
// not a condition. The caller holds r.claim (see withMigration).
func (r *Runner) settleable(ctx context.Context, user string, m core.Migration) (settle, error) {
	if !m.LeftOut() {
		return settle{}, core.ErrNoProblemsToSettle
	}
	v, _ := r.store.LatestVerification(ctx, user, core.TrackPhotos)
	return settle{prev: v, fromDrive: core.DecodeProgress(m.PhotosProgress).FromDrive()}, nil
}

// AcceptPhotos ends the Photos half as done although the import left files
// out, because the person looked at them and said so. It then does what any
// done does: credentials no track needs go, and the staging retention starts.
func (r *Runner) AcceptPhotos(ctx context.Context, user string) error {
	var s settle
	err := r.withMigration(ctx, user, func(m core.Migration) error {
		var err error
		if s, err = r.settleable(ctx, user, m); err != nil {
			return err
		}
		_, err = r.store.SetPhotosState(ctx, user, core.PhotosDone, core.EncodeProgress(core.PhotosDoneAccepted(s.fromDrive)))
		return err
	})
	if err != nil {
		return err
	}

	r.log.Info("photos accepted with files left out", "user", user, "files", s.prev.Mismatch)
	r.photosFinished(ctx, user, "You accepted the import as it is, with the files it left out.", s.fromDrive)
	return nil
}

// RetryPhotosProblems sends again only the files the last import left out,
// launched like the card's other retry (see launchFromWizard), and only when
// core.Verify.CanRetryProblems allows it: retrying part of what was left out
// and calling the track done would drop the rest without a word.
func (r *Runner) RetryPhotosProblems(ctx context.Context, user string) error {
	var s settle
	return r.launchFromWizard(ctx, user, func(m core.Migration) error {
		var err error
		if s, err = r.settleable(ctx, user, m); err != nil {
			return err
		}
		if !s.prev.CanRetryProblems() {
			return core.ErrProblemsIncomplete
		}
		return nil
	}, func(ctx context.Context) { r.runProblemsRetry(ctx, user, s) })
}

// runProblemsRetry copies the files out of the archives into a folder of their
// own and imports that folder. immich-go's folder mode needs no Google
// metadata, which is what left a file like "IMG(1).MP4" pending in the first
// place: it gets uploaded, or recognised as a duplicate of one already there.
// The track is already claimed as importing.
func (r *Runner) runProblemsRetry(ctx context.Context, user string, s settle) {
	r.acquire()
	defer r.release()
	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()

	creds, err := r.ImmichKey(ctx, user)
	if err != nil {
		r.failPhotos(ctx, user, core.Progress{Key: core.FailImmichMissing})
		r.log.Error("retry problems: no Immich key", "user", user, "error", err)
		return
	}

	staging := filepath.Join(r.cfg.StagingDir, core.SafeName(user))
	// A copy folder left by a retry a restart cut short is removed first: the
	// track is claimed, so no other retry of this person's can be using it.
	if old, _ := filepath.Glob(filepath.Join(staging, ".retry-*")); len(old) > 0 {
		for _, d := range old {
			_ = os.RemoveAll(d)
		}
	}
	dir, err := os.MkdirTemp(staging, ".retry-")
	if err != nil {
		r.log.Error("retry problems: create folder", "user", user, "error", err)
		r.restoreLeftOut(ctx, user, s)
		return
	}
	defer os.RemoveAll(dir)

	missing := extractProblems(staging, dir, s.prev.Problems)
	for _, p := range missing {
		r.log.Warn("retry problems: not found in the archives", "user", user, "file", p.File)
	}
	if len(missing) == len(s.prev.Problems) {
		// Nothing to send: the card says so for each file, and stops offering
		// a retry that would find nothing again.
		v := s.prev
		v.User, v.Track, v.Problems = user, core.TrackPhotos, missing
		if err := r.store.PutVerification(ctx, v); err != nil {
			r.log.Error("retry problems: store verification", "user", user, "error", err)
		}
		r.restoreLeftOut(ctx, user, s)
		return
	}

	logFile := immichLogFile(staging)
	r.log.Info("photos retry started", "user", user, "files", len(s.prev.Problems)-len(missing), "log", logFile)
	r.importWithImmichGo(ctx, user, creds, "from-folder", nil, []string{dir}, photosRun{
		fromDrive: s.fromDrive, logFile: logFile,
		retry: &retryRun{settle: s, missing: missing},
	})
}

// restoreLeftOut puts Photos back where a retry of the files left out found
// it, when the retry stopped before it could say anything new about them: the
// folder could not be made, none of the files was in the archives, or
// immich-go did not get to its report. The files, the accept and the retries
// stay on the card; another reason there would take them away.
func (r *Runner) restoreLeftOut(ctx context.Context, user string, s settle) {
	r.failPhotos(ctx, user, core.ImportErrors(0, s.prev.Mismatch, s.fromDrive))
}

// extractProblems copies each problem file out of its archive into dir and
// returns the ones it could not copy, marked Missing: they stay left out. A problem's File is
// immich-go's "<archive>:<path>", the archive being the zip's name without
// ".zip". File i goes to "<i>/<name>", so two parts holding the same name
// cannot overwrite each other, and originOf can map it back. Each archive is
// opened once, however many of its files are asked for.
func extractProblems(staging, dir string, problems []core.Problem) []core.Problem {
	byArchive := map[string][]int{}
	var missing []core.Problem
	for i, p := range problems {
		archive, _, ok := strings.Cut(p.File, ":")
		if !ok {
			missing = append(missing, notFound(p))
			continue
		}
		byArchive[archive] = append(byArchive[archive], i)
	}
	for archive, indexes := range byArchive {
		copied := extractFrom(filepath.Join(staging, filepath.Base(archive)+".zip"), dir, problems, indexes)
		for _, i := range indexes {
			if !copied[i] {
				missing = append(missing, notFound(problems[i]))
			}
		}
	}
	return missing
}

// notFound marks a problem as not found in the archives.
func notFound(p core.Problem) core.Problem {
	p.Missing = true
	return p
}

// extractFrom copies the listed problems' entries out of one zip, and reports
// which it copied.
func extractFrom(archive, dir string, problems []core.Problem, indexes []int) map[int]bool {
	copied := map[int]bool{}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return copied
	}
	defer zr.Close()
	entries := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		entries[f.Name] = f
	}
	for _, i := range indexes {
		_, name, _ := strings.Cut(problems[i].File, ":")
		f, ok := entries[name]
		if !ok {
			continue
		}
		if copyEntry(f, filepath.Join(dir, strconv.Itoa(i), filepath.Base(name))) == nil {
			copied[i] = true
		}
	}
	return copied
}

// copyEntry writes one zip entry to dest.
func copyEntry(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// The zip's own date, not now: folder mode falls back to the file's date
	// when the file holds none, and a video taken in 2023 must not land in
	// Immich as taken today.
	return os.Chtimes(dest, f.Modified, f.Modified)
}

// originOf maps a file a retry names back to the one in the archives it was
// copied from. The retry folder holds problems[i] as "<i>/<name>", so the
// folder above the name is the index, whatever prefix immich-go puts before
// it. A name that does not fit is kept as it is.
func originOf(file string, problems []core.Problem) string {
	parts := strings.Split(filepath.ToSlash(file), "/")
	if len(parts) < 2 {
		return file
	}
	index := parts[len(parts)-2]
	if i := strings.LastIndex(index, ":"); i >= 0 {
		index = index[i+1:]
	}
	if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(problems) &&
		filepath.Base(filepath.ToSlash(problems[i].File)) == parts[len(parts)-1] {
		return problems[i].File
	}
	return file
}

// sentOf is the problems a retry sent: all of them but the missing ones.
func sentOf(problems, missing []core.Problem) []core.Problem {
	skip := map[string]bool{}
	for _, p := range missing {
		skip[p.File] = true
	}
	var sent []core.Problem
	for _, p := range problems {
		if !skip[p.File] {
			sent = append(sent, p)
		}
	}
	return sent
}
