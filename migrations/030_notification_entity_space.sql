-- +goose Up
-- +goose StatementBegin

-- Denormalise the entity's space onto the notification so the bell can build
-- a route to it. Notifications are user-scoped (no space_id, and they consult
-- no readable set); a notification carries entity_kind + entity_id, but every
-- entity read endpoint is space-scoped, so the client cannot resolve a ticket
-- UUID to its space without already knowing the space. Capturing the space at
-- creation (when the actor had access) mirrors how audit_log denormalises
-- entity_kind/entity_id, adds no new read path, and creates no permission
-- oracle: clicking navigates to the normal space-scoped detail page, which
-- enforces read authz subject-side and 404s a deleted / no-longer-accessible
-- entity.
--
-- Nullable, no backfill: legacy rows simply stay non-routable and the bell
-- degrades to mark-read-only for them.
ALTER TABLE notifications ADD COLUMN entity_space_id UUID;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notifications DROP COLUMN entity_space_id;
-- +goose StatementEnd
