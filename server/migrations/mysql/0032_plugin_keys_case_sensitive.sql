-- plugin_kv.k and plugin_docs.id are case-sensitive identifiers on SQLite
-- and PostgreSQL (byte/TEXT comparison): `Count` and `count` are two slots,
-- and a prefix listing must not swallow the other case. MySQL's default
-- utf8mb4 collation folds case, so the composite PK treated them as one row
-- (the upsert silently overwrote one with the other) and `LIKE 'Co%'`
-- returned both. Column-level utf8mb4_bin restores the shared semantics;
-- the unique prefixes only widen (bin distinguishes MORE strings), so
-- existing rows stay valid. MySQL tree only — the other two engines are
-- already case-sensitive.
ALTER TABLE plugin_kv MODIFY k VARCHAR(191) COLLATE utf8mb4_bin NOT NULL;
ALTER TABLE plugin_docs MODIFY id VARCHAR(191) COLLATE utf8mb4_bin NOT NULL;
