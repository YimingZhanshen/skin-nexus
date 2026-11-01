-- Plugin provenance (v0.4.0): distinguish trusted market/upload bundles from
-- locally confirmed unverified installs. One column per statement, as in the
-- other trees.
ALTER TABLE plugin_meta ADD COLUMN trust VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE plugin_meta ADD COLUMN signer VARCHAR(64) NOT NULL DEFAULT '';
