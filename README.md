<p align="center">
  <img src="brand/wordmark.svg" alt="zlatan" width="320">
</p>

# zlatan

> Status: **v0.6.1** (2026-09-26). The service runs, the schema is applied, the
> wizard renders and gates access; the Google OAuth flow, the Nextcloud Login
> Flow (per-person app password), the runner that drives `rclone` straight into
> Nextcloud over WebDAV, the runner that drives `immich-go`, the resumable
> Takeout upload and the watcher that collects a Takeout from the person's own
> Drive all work. A finished copy is now **verified** (whole tree by size, a
> sample byte for byte), **notified** over ntfy, and its staging is **purged**
> on a retention clock; before the copy Zlatan reads the source size and the
> person's Nextcloud usage and **warns** if the copy would exceed their budget.
> The image is published at `ghcr.io/marcodellemarche/zlatan`. Each person
> connects their own Immich API key, so their photos land in their own
> account, and the wizard links them to the public address of each cloud, never
> the internal Docker one. See "What is missing".

zlatan is a self-guided migration service for self-hosted stacks. A person who
is not technical — a family member, a friend — opens it in a browser, signs in
with the household single sign-on, and is walked through moving their data off
Google: **Google Drive → Nextcloud** and **Google Photos → Immich**. The
operator configures it once and is not in the loop for every person.

## The constraint that defines the service

**Drive can be automated end to end. Photos cannot.** On 2025-03-31 Google
removed the read scopes of the Photos Library API, and there is no API to start
a Takeout (`takeout.google.com` is not programmable; the Data Portability API
covers only Chrome, Maps, Play, Search, Shopping and YouTube). So the Photos
route always contains **one human click**: the wizard guides that person, works
out on its own when the export is ready, and does everything else. This is not
a limit that more code can remove — it is Google.

## How it is built

A single Go binary (`zlatan`), inside a container that also holds `rclone` and
`immich-go`. SQLite for the state: a migration lasts hours or days and cannot
live inside an HTTP request.

```
internal/
├── core/       domain types, token encryption (AES-GCM), the shared sanitizer
├── config/     configuration from the environment, every error in one pass
├── store/      SQLite, migrations, repository
├── oauth/      the Google client, sealed tokens
├── nextcloud/  Login Flow v2, per-person app passwords
├── runner/     drives rclone and immich-go
├── upload/     the Takeout upload, in chunks, resumable
└── web/        the wizard: routes, authentication, templates
```

Two independent tracks, `drive` and `photos`: a person may run one, the other,
or both in parallel. The state of one never touches the other.

## How the Photos half collects a Takeout

Google cannot be asked for a Takeout by a program, so the person asks for it
once, choosing **"Add to Drive"**. That puts the export in their own Drive,
under a folder Google names `Takeout`. Zlatan already holds a `drive.readonly`
token for that account from the Drive half, so a watcher simply looks for the
folder: when it appears, and only once every part Google listed is present and
non-empty, it downloads the parts and imports them with `immich-go`.

Nothing is shared with anybody: there is no central Google account and no
folder-sharing step. The Drive copy excludes the `Takeout` folder, so the
archive does not also land in Nextcloud as files — the photos belong in Immich
and the archive is disposable. A wait that outlives `ZLATAN_TAKEOUT_MAX_WAIT`
ends with a pointer to the upload route, rather than a screen that never moves.

## How the Drive half reaches Nextcloud

rclone copies Google Drive **straight into the person's Nextcloud over
WebDAV**, so there is no local staging for the Drive half and no second import
step. The write credential is a per-user **app password obtained through
Nextcloud's Login Flow v2**: the person clicks "Grant access" once, behind the
same SSO, and Zlatan receives a credential with exactly that person's rights.

This was chosen over the alternatives on purpose. The Nextcloud admin over
WebDAV can *read* another person's files but cannot *write* into their space
(MKCOL answers 403), so an admin-driven import does not work. The direct
filesystem route (write into the data directory and run `occ files:scan`)
would need the Docker socket — root-equivalent access on the host — and a
coupling to Nextcloud's internal layout. The Login Flow needs neither: only
public APIs, and no standing privilege beyond what each person already has.

## Security

The service **holds the Google OAuth refresh token of every person who uses
it**, and each one grants read access to that person's entire Drive. It also
holds each person's Nextcloud app password. They are the most sensitive
secrets in the homelab.

- **Tokens are encrypted at rest** (AES-GCM). The key (`ZLATAN_TOKEN_KEY`) lives
  only in the environment; the database holds ciphertext.
- **The identity comes only from the forward-auth header** (`Remote-User`), and
  is believed only when the request arrives from the configured proxy network.
  Never from a URL parameter or a cookie: a person can only ever see their own
  migration, by construction.
- **A secret shared with the proxy** (`X-Zlatan-Proxy-Secret`): a container on
  the same Docker network cannot reach the service directly and skip the SSO.
- **A public bind without a secret is refused at startup**, not accepted
  silently.

## Configuration

See `.env.example`. The required variables are `ZLATAN_TRUSTED_PROXY`,
`ZLATAN_TOKEN_KEY` and — if the bind is public — `ZLATAN_PROXY_SECRET`.

## Commands

```sh
zlatan serve      # migrate the schema, then listen
zlatan migrate    # apply pending database migrations and exit
zlatan version
```

## Development

```sh
make check    # gofmt, vet, tests (with the race detector)
make image    # build the image
```

## What is missing

- [x] Per-person Google OAuth (`drive.readonly`), tokens sealed in the store.
- [x] A `Runner` that drives `rclone` for Drive → Nextcloud, with progress.
- [x] A `Runner` that drives `immich-go` for the Takeout → Immich.
- [x] Resumable Takeout upload (route B).
- [x] Import into Nextcloud: rclone copies Drive straight to WebDAV, with a
      per-person app password from the Nextcloud Login Flow v2.
- [x] The "Add to Drive" route: a watcher looks for the Takeout folder in the
      person's own Drive (with the token it already holds), waits until every
      part is complete, downloads it and imports it. No folder to share and no
      central account.
- [x] Verification: the whole tree by size, plus a random sample compared byte
      for byte (Drive exposes MD5, Nextcloud SHA1, so no shared hash exists and
      the sample is downloaded). Photos are checked through `immich-go`'s own
      report, which hashes each asset against the server.
- [x] Staging purge: a finished migration's staging is removed after the
      configured retention, swept from the database so a directory nobody owns
      is never touched.
- [x] Notifications over ntfy on completion and on failure.
- [x] Quotas: the warning (source size + current Nextcloud usage against the
      budget) is implemented.
- [x] A per-person Immich API key. Immich has no admin endpoint that mints a
      key for another account, so the person creates their own in Immich and
      pastes it into the wizard; Zlatan validates it against `/api/users/me`,
      shows whose account it is, and seals it per person. The import runs as
      them, so photos land in their own library and no shared key exists to
      misfile anyone.

The full design lives in the homelab repository, `docs/zlatan-service.md`.
