-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- One row per person, keyed by the forward-auth identity. Drive and Photos are
-- two independent state machines on the same row: a person may run one, the
-- other, or both.
CREATE TABLE migrations (
    user             TEXT PRIMARY KEY,
    email            TEXT NOT NULL DEFAULT '',

    drive_state      TEXT NOT NULL DEFAULT 'not_started',
    photos_state     TEXT NOT NULL DEFAULT 'not_started',

    drive_progress   TEXT NOT NULL DEFAULT '',
    photos_progress  TEXT NOT NULL DEFAULT '',

    drive_bytes_copied  INTEGER NOT NULL DEFAULT 0,
    drive_files_copied  INTEGER NOT NULL DEFAULT 0,
    photos_assets_added INTEGER NOT NULL DEFAULT 0,

    last_error       TEXT NOT NULL DEFAULT '',

    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

-- OAuth tokens, sealed at rest. The plaintext never reaches this table: the
-- column holds AES-GCM ciphertext, and the key lives only in the environment.
CREATE TABLE tokens (
    user        TEXT NOT NULL,
    provider    TEXT NOT NULL,
    sealed      BLOB NOT NULL,
    scopes      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (user, provider)
);

-- Verification results, one row per check, so the closing page can state what
-- was actually compared rather than "done".
CREATE TABLE verifications (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user        TEXT NOT NULL,
    track       TEXT NOT NULL,
    checked     INTEGER NOT NULL DEFAULT 0,
    matched     INTEGER NOT NULL DEFAULT 0,
    mismatch    INTEGER NOT NULL DEFAULT 0,
    detail      TEXT NOT NULL DEFAULT '',
    checked_at  TEXT NOT NULL
);

CREATE INDEX verifications_user_track ON verifications (user, track, checked_at DESC);

-- +goose Down
DROP TABLE verifications;
DROP TABLE tokens;
DROP TABLE migrations;
