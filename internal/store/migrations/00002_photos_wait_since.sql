-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- When the Photos half started waiting for a Takeout, so the watcher can give
-- up after a while instead of watching forever. It cannot reuse updated_at:
-- that column moves for the Drive track too, so a Drive progress update would
-- silently reset the Photos wait and the person would never be told to fall
-- back to the upload route.
ALTER TABLE migrations ADD COLUMN photos_wait_since TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE migrations DROP COLUMN photos_wait_since;
