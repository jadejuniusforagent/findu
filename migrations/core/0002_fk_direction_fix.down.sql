-- 0002 down：恢复外键（NOT VALID，避免历史孤儿行导致回滚失败）
-- 同样做表存在性守卫，保证在干净库上可回滚。
BEGIN;

DO $$
BEGIN
    IF to_regclass('public.mcp_servers') IS NOT NULL AND to_regclass('public.sync_state') IS NOT NULL THEN
        ALTER TABLE sync_state DROP CONSTRAINT IF EXISTS sync_state_datasource_id_fkey;
        ALTER TABLE sync_state
            ADD CONSTRAINT sync_state_datasource_id_fkey
            FOREIGN KEY (datasource_id) REFERENCES mcp_servers(id) NOT VALID;
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.traffic_evidence_snapshots') IS NOT NULL AND to_regclass('public.finding_traffic_bindings') IS NOT NULL THEN
        ALTER TABLE finding_traffic_bindings DROP CONSTRAINT IF EXISTS finding_traffic_bindings_snapshot_id_fkey;
        ALTER TABLE finding_traffic_bindings
            ADD CONSTRAINT finding_traffic_bindings_snapshot_id_fkey
            FOREIGN KEY (snapshot_id) REFERENCES traffic_evidence_snapshots(id) NOT VALID;
    END IF;
END
$$;

DELETE FROM schema_migrations WHERE version = '0002_fk_direction_fix';
COMMIT;
