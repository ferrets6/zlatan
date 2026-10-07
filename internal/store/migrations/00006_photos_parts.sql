-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- How many archives the person said Google split their Takeout into. Nothing
-- inside the archives carries the count, so the upload route asks for it and
-- starts the import only once that many are here. 0 means not declared yet.
ALTER TABLE migrations ADD COLUMN photos_parts_expected INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE migrations DROP COLUMN photos_parts_expected;
