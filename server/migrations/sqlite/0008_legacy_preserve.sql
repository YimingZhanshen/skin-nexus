-- Legacy-preserving columns (data-compat rule 7), same set as
-- migrations/mysql/0008_legacy_preserve.sql. One column per statement so the
-- tracked migrator can tolerate each collision on its own.
ALTER TABLE users ADD COLUMN score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN locale VARCHAR(255) NULL;
ALTER TABLE users ADD COLUMN is_dark_mode INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN permission INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN last_sign_at DATETIME NULL;
ALTER TABLE users ADD COLUMN verification_token VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE textures ADD COLUMN public INTEGER NOT NULL DEFAULT 1;
