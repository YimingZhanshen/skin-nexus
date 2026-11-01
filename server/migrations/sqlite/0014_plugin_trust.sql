-- Plugin provenance (v0.4.0): trusted market/upload bundles vs locally
-- confirmed unverified installs. SQLite takes one ADD COLUMN per statement.
ALTER TABLE plugin_meta ADD COLUMN trust VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE plugin_meta ADD COLUMN signer VARCHAR(64) NOT NULL DEFAULT '';
