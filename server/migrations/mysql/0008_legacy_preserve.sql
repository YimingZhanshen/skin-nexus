-- Legacy-preserving columns (data-compat rule 7). Importer mapping from the
-- 2026-10-23 production recon (legacy DB): users score/locale/is_dark_mode/
-- permission/last_sign_at/verification_token + textures.public (21 of 59
-- legacy textures are private — live privacy state, must not be lost).
-- Serving-side enforcement of `public` is a separate P2 feature (needs live
-- 403 semantics evidence); this migration only preserves the flag.
ALTER TABLE users ADD COLUMN score INT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN locale VARCHAR(255) NULL;
ALTER TABLE users ADD COLUMN is_dark_mode TINYINT(1) NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN permission INT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN last_sign_at DATETIME NULL;
ALTER TABLE users ADD COLUMN verification_token VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE textures ADD COLUMN public TINYINT(1) NOT NULL DEFAULT 1;
