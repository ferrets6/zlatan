// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/immich"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// googleProvider is the key the Google token is stored under.
const googleProvider = "google"

// A credential is kept only while a track can still use it. Each one belongs
// to a track, except the Google token, which both use (the Drive copy, and the
// "Add to Drive" route of Photos):
//
//   - Drive done: the Nextcloud app password is revoked and forgotten.
//   - Photos done: the Immich key is forgotten. Immich offers no way to revoke
//     a key without its id, so the person deletes it in Immich; the
//     notification tells them.
//   - Both done: the Google token is revoked at Google and forgotten.
//   - Nothing moved for the retention period and no work in flight: everything
//     is forgotten. That covers whoever moves only one half, and whoever stops
//     halfway, who would otherwise leave a token here for good.
//
// A failed track keeps its credentials, so "try again" works without
// reconnecting; the idle sweep is what eventually clears an abandoned one.

// releaseFinished forgets the credentials no track needs any more, after a
// track reached done. Failures are logged, never returned: the migration
// already succeeded, and a credential left behind is swept later.
func (r *Runner) releaseFinished(ctx context.Context, user string) {
	m, err := r.store.GetMigration(ctx, user)
	if err != nil {
		r.log.Error("release credentials: read migration", "user", user, "error", err)
		return
	}
	driveDone := m.DriveState == core.DriveDone
	photosDone := m.PhotosState == core.PhotosDone
	if driveDone {
		r.forgetNextcloud(ctx, user)
	}
	if photosDone {
		r.forget(ctx, user, immich.Provider)
	}
	if driveDone && photosDone {
		r.forgetGoogle(ctx, user)
	}
}

// forgetAll forgets every credential a person has given.
func (r *Runner) forgetAll(ctx context.Context, user string) {
	r.forgetNextcloud(ctx, user)
	r.forget(ctx, user, immich.Provider)
	r.forgetGoogle(ctx, user)
}

// forgetGoogle revokes the Google grant and forgets the token. Revoking is
// best effort: forgetting it is what takes away Zlatan's access, and revoking
// also removes the grant from the person's Google account. A token that no
// longer opens is still forgotten.
func (r *Runner) forgetGoogle(ctx context.Context, user string) {
	tok, err := r.store.GetToken(ctx, user, googleProvider)
	if err != nil {
		return
	}
	if r.google != nil {
		if tokens, err := r.openTokens(tok); err == nil && tokens.RefreshToken != "" {
			rctx, cancel := context.WithTimeout(ctx, probeTimeout)
			if err := r.google.Revoke(rctx, tokens.RefreshToken); err != nil {
				r.log.Warn("release credentials: revoke the Google grant", "user", user, "error", err)
			}
			cancel()
		}
	}
	r.drop(ctx, user, googleProvider)
}

// forgetNextcloud deletes the app password in Nextcloud, so it stops working
// there too, and forgets it. The Login Flow token that may be left from an
// unfinished grant goes with it.
func (r *Runner) forgetNextcloud(ctx context.Context, user string) {
	if tok, err := r.store.GetToken(ctx, user, nextcloud.Provider); err == nil {
		if r.nc != nil {
			if creds, err := nextcloud.OpenCredentials(r.sealer, tok.Sealed); err == nil {
				rctx, cancel := context.WithTimeout(ctx, probeTimeout)
				if err := r.nc.RevokeAppPassword(rctx, creds); err != nil {
					r.log.Warn("release credentials: delete the Nextcloud app password", "user", user, "error", err)
				}
				cancel()
			}
		}
		r.drop(ctx, user, nextcloud.Provider)
	}
	r.forget(ctx, user, nextcloudFlowProvider)
}

// drop deletes a token the caller has already read (so it is known to exist),
// without a second GetToken. forgetGoogle and forgetNextcloud use it after
// reading the token to revoke it.
func (r *Runner) drop(ctx context.Context, user, provider string) {
	if err := r.store.DeleteToken(ctx, user, provider); err != nil {
		r.log.Error("release credentials: delete", "user", user, "provider", provider, "error", err)
		return
	}
	r.log.Info("credential forgotten", "user", user, "provider", provider)
}

// forget deletes a token that the caller has not already read: it checks it is
// there first, so it logs "forgotten" only when there was something to forget.
func (r *Runner) forget(ctx context.Context, user, provider string) {
	if _, err := r.store.GetToken(ctx, user, provider); err != nil {
		// Nothing stored: the common case, nothing to do and nothing to say.
		if errors.Is(err, store.ErrNoToken) {
			return
		}
		// A read error (e.g. the store is momentarily busy): do not delete on a
		// guess, but say so — the next sweep retries. Silence here would hide a
		// credential that was meant to be forgotten and was not.
		r.log.Error("release credentials: read before delete", "user", user, "provider", provider, "error", err)
		return
	}
	r.drop(ctx, user, provider)
}

// credentialSweep is how often the idle sweep runs. It is its own clock, not
// the staging retention's: the staging sweeper is disabled when retention is 0
// ("keep nothing"), and that must not also switch off the only thing that
// forgets the credentials of whoever abandoned a migration.
const credentialSweep = time.Hour

// credentialIdle is how long a migration must sit still before its credentials
// are forgotten. It follows the staging retention, but never goes below a floor:
// retention 0 means "keep no staging" (the staging sweeper is disabled), and
// that must NOT be read as "forget credentials the instant someone connects
// them". Someone who has just granted Google/Nextcloud/Immich but not pressed
// Start is idle by state, not abandoned.
func credentialIdle(retention time.Duration) time.Duration {
	floor := time.Duration(config.DefaultRetentionDays) * 24 * time.Hour
	if retention < floor {
		return floor
	}
	return retention
}

// WatchCredentials forgets, on its own clock, the credentials of everyone whose
// migration has gone idle. It runs for the life of the process, independent of
// the staging sweeper, so it keeps working even when staging retention is 0.
func (r *Runner) WatchCredentials(ctx context.Context) {
	ticker := time.NewTicker(credentialSweep)
	defer ticker.Stop()

	idle := credentialIdle(r.cfg.StagingRetention)
	r.log.Info("credential sweeper started", "interval", credentialSweep, "idle", idle)
	r.sweepIdle(ctx, idle)
	for {
		select {
		case <-ctx.Done():
			r.log.Info("credential sweeper stopped")
			return
		case <-ticker.C:
			r.sweepIdle(ctx, idle)
		}
	}
}

// sweepIdle forgets the credentials of everyone whose migration has not moved
// for longer than idle and has no work in flight.
func (r *Runner) sweepIdle(ctx context.Context, idle time.Duration) {
	users, err := r.store.ListIdle(ctx, time.Now().Add(-idle))
	if err != nil {
		r.log.Error("credential sweeper: list", "error", err)
		return
	}
	for _, user := range users {
		r.forgetAll(ctx, user)
		// Bump the row so this sweep does not keep re-selecting and re-forgetting
		// the same abandoned migration every hour for the life of the process.
		if err := r.store.Touch(ctx, user); err != nil {
			r.log.Error("credential sweeper: touch", "user", user, "error", err)
		}
	}
}
