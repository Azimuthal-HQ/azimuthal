-- Retire the page-lock table (S2).
--
-- page_locks was advisory only. No write path ever consulted it: the sole
-- SELECT against the table (GetPageLock) was reached from exactly two places,
-- both inside the lock service itself, and neither the markdown save path
-- (internal/core/wiki/page.go) nor the Codex document path
-- (internal/core/wiki/document.go) read it. Page-write concurrency is
-- protected by optimistic version checks — UpdatePageContent's
-- `WHERE id = $1 AND version = $2` and the document path's base_version
-- guard — so dropping this table weakens no write guarantee.
--
-- It was also unbounded: LockService.PurgeExpired and DeleteExpiredPageLocks
-- were never scheduled by cmd/server/main.go, so expired rows accumulated
-- for the lifetime of the deployment.
--
-- The routes it backed had no frontend caller. Per-author drafts plus the
-- version guard superseded the advisory lock in PR #73/#75, which left the
-- table in place deliberately because retiring shipped API is a maintainer's
-- call; this migration lands that decision.
--
-- Migration 013 is left untouched — numbering is immutable once shipped.

-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS page_locks;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- A genuine inverse of 013, index included, so a rollback restores the
-- schema this migration found rather than an approximation of it.
CREATE TABLE page_locks (
    page_id     UUID        PRIMARY KEY REFERENCES pages(id) ON DELETE CASCADE,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_name   TEXT        NOT NULL DEFAULT '',
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_page_locks_expires ON page_locks (expires_at);

-- +goose StatementEnd
