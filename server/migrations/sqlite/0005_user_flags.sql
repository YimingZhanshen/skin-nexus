-- Schema-alignment fix (found by the CI MySQL integration suite, 2026-10-23):
-- users.email_verified and users.can_join_server were always read/written by
-- the store but missing from 0001. Each ALTER stands alone so a collision on
-- one column cannot hide the other (review A2 2026-10-23).
ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN can_join_server INTEGER NOT NULL DEFAULT 1;
