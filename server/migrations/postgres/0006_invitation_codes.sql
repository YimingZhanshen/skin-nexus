-- invitation-codes (data-compat rule 7: 1:1 with the live blessing plugin
-- schema). Legacy ids are imported explicitly, so the sequence must be resynced
-- after an import.
CREATE TABLE IF NOT EXISTS invitation_codes (
  id           BIGSERIAL PRIMARY KEY,
  code         VARCHAR(255) NOT NULL,
  generated_at TIMESTAMPTZ NOT NULL,
  used_by      BIGINT NOT NULL DEFAULT 0,
  used_at      TIMESTAMPTZ NULL,
  description  VARCHAR(255) NULL
);
