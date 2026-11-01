-- invitation-codes (data-compat rule 7: 1:1 with the live blessing plugin
-- schema). Legacy ids are imported explicitly, so id stays a plain rowid.
CREATE TABLE IF NOT EXISTS invitation_codes (
  id           INTEGER PRIMARY KEY,
  code         VARCHAR(255) NOT NULL,
  generated_at DATETIME NOT NULL,
  used_by      INTEGER NOT NULL DEFAULT 0,
  used_at      DATETIME NULL,
  description  VARCHAR(255) NULL
);
