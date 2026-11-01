-- invitation-codes (data-compat rule 7: 1:1 with the live blessing plugin
-- schema — callbacks.php creates id/code/generated_at/used_by/used_at and
-- adds description via Schema::table; description carries the legacy
-- human-readable note convention consumed by the old /code/ query page).
-- IF NOT EXISTS: legacy-migrated databases may already carry the table.
CREATE TABLE IF NOT EXISTS invitation_codes (
  id           INT UNSIGNED NOT NULL AUTO_INCREMENT,
  code         VARCHAR(255) NOT NULL,
  generated_at DATETIME     NOT NULL,
  used_by      INT          NOT NULL DEFAULT 0,
  used_at      DATETIME     NULL,
  description  VARCHAR(255) NULL,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
