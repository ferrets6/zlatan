// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// purgeInterval is how often the sweeper looks for staging to remove. The
// retention is measured in days, so an hourly sweep is plenty and costs
// nothing.
const purgeInterval = time.Hour

// WatchStaging removes a person's staging directory once their Photos half
// (the only one that stages anything) has ended, done, failed or cancelled,
// for longer than the configured retention. It runs until ctx is cancelled.
//
// Staging is a copy of data that is already in Nextcloud and Immich, so it is
// disposable by design; the retention exists only to answer "wait, did my
// video arrive?" before the local copy goes. It is swept from the database,
// not from the filesystem, so a directory nobody owns any more is not touched
// by accident.
func (r *Runner) WatchStaging(ctx context.Context) {
	retention := r.cfg.StagingRetention
	if retention <= 0 {
		r.log.Info("staging sweeper disabled", "retention", retention)
		return
	}

	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()

	r.log.Info("staging sweeper started", "retention", retention, "interval", purgeInterval)
	// One pass at startup, so a service restarted after the retention elapsed
	// does not wait a whole interval to clean up.
	r.purgeStaging(ctx)
	for {
		select {
		case <-ctx.Done():
			r.log.Info("staging sweeper stopped")
			return
		case <-ticker.C:
			r.purgeStaging(ctx)
		}
	}
}

func (r *Runner) purgeStaging(ctx context.Context) {
	// What a purge moved aside and could not finish removing (a restart, a
	// file that would not go) is removed first, or it would stay for good:
	// nothing else ever looks at it.
	if root, err := filepath.Abs(r.cfg.StagingDir); err == nil {
		aside, _ := filepath.Glob(filepath.Join(root, ".purge-*"))
		for _, dir := range aside {
			if err := os.RemoveAll(dir); err != nil {
				r.log.Error("staging sweeper: remove a leftover", "dir", dir, "error", err)
			}
		}
	}

	finished, err := r.store.ListFinished(ctx)
	if err != nil {
		r.log.Error("staging sweeper: list", "error", err)
		return
	}
	retention := r.cfg.StagingRetention
	now := time.Now()
	for _, m := range finished {
		// A row the stamp has not reached (see FinishedMigration). Stamp it
		// now and skip: it starts its retention window today rather than being
		// purged the moment the service is upgraded.
		if m.FinishedAt.IsZero() {
			if err := r.store.StampFinished(ctx, m.User); err != nil {
				r.log.Error("staging sweeper: stamp", "user", m.User, "error", err)
			}
			continue
		}
		if !expired(m.FinishedAt, retention, now) {
			continue
		}
		if err := r.purgeUser(ctx, m.User); err != nil {
			r.log.Error("staging sweeper: purge", "user", m.User, "error", err)
		}
	}
}

// purgeUser removes one person's staging directory, staying inside the staging
// root. The name goes through the same sanitizer the upload and runner use, so
// the directory removed is exactly the one they wrote to.
func (r *Runner) purgeUser(ctx context.Context, user string) error {
	dir := filepath.Join(r.cfg.StagingDir, core.SafeName(user))

	root, err := filepath.Abs(r.cfg.StagingDir)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// Belt to the sanitizer's braces: never remove anything outside the root,
	// and never the root itself.
	if abs == root || !isUnder(abs, root) {
		r.log.Error("staging sweeper: refusing a path outside the staging root", "path", abs)
		return nil
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	// The sweep listed this person as ended, but since then a retry may have
	// claimed the track, or run and ended again with a fresh window. Check
	// both again under the claim lock, and move the directory aside there, so
	// no claim can slip between the check and the removal; the removal itself,
	// which can take a while for a large export, runs after the lock is let
	// go. Anything that cannot be read keeps the files. SafeName never yields
	// a dot, so the aside name cannot be anyone's directory.
	r.claim.Lock()
	if !r.stillExpired(ctx, user) {
		r.claim.Unlock()
		return nil
	}
	aside := filepath.Join(root, ".purge-"+core.SafeName(user)+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	err = os.Rename(dir, aside)
	r.claim.Unlock()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(aside); err != nil {
		return err
	}
	r.log.Info("staging purged", "user", user, "dir", dir)
	return nil
}

// expired is the staging retention rule: ended, stamped, and the window gone
// by. The sweep and its recheck under the lock both judge by it.
func expired(finishedAt time.Time, retention time.Duration, now time.Time) bool {
	return !finishedAt.IsZero() && now.Sub(finishedAt) >= retention
}

// stillExpired reports whether the person's Photos half is still ended and
// past the retention, read afresh. Anything it cannot read answers no: the
// cost of a wrong yes is the archives a retry needs. The caller holds r.claim.
func (r *Runner) stillExpired(ctx context.Context, user string) bool {
	m, err := r.store.GetMigration(ctx, user)
	return err == nil && m.PhotosState.Terminal() &&
		expired(m.PhotosFinishedAt, r.cfg.StagingRetention, time.Now())
}

func isUnder(path, root string) bool {
	return len(path) > len(root) && path[:len(root)+1] == root+string(os.PathSeparator)
}
