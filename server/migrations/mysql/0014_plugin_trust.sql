-- Plugin provenance (v0.4.0): distinguish trusted market/upload bundles from
-- locally confirmed unverified installs. DDL-only by convention; existing rows
-- keep empty trust/signer and surface as "not recorded" until reinstalled.
ALTER TABLE plugin_meta
  ADD COLUMN trust  VARCHAR(16) NOT NULL DEFAULT '' AFTER source,
  ADD COLUMN signer VARCHAR(64) NOT NULL DEFAULT '' AFTER trust;
