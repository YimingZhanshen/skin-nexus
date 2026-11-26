-- Server Access Slice S6a: additive deletion lifecycle timestamps.
-- deleted_at remains reserved for physical deletion; grace-period state is explicit.
ALTER TABLE sa_tenants
  ADD COLUMN deletion_requested_at DATETIME(3) NULL,
  ADD COLUMN deletion_due_at DATETIME(3) NULL;
