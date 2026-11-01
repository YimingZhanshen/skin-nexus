-- Legacy-preserving columns (data-compat rule 7), same set as
-- migrations/mysql/0008_legacy_preserve.sql. One column per statement: PG
-- accepts a comma list, SQLite does not, and the tracked migrator tolerates a
-- collision per statement — so splitting keeps both trees converging the same
-- way on a partially-migrated database.
ALTER TABLE users ADD COLUMN score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN locale VARCHAR(255) NULL;
ALTER TABLE users ADD COLUMN is_dark_mode SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN permission INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN last_sign_at TIMESTAMPTZ NULL;
ALTER TABLE users ADD COLUMN verification_token VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE textures ADD COLUMN public SMALLINT NOT NULL DEFAULT 1;
