-- 0002 修正两条外键方向违规（core/domain 不得引用 plugin 表）
-- 做法：去掉外键约束，保留字段作为逻辑引用；取值前由应用层判断插件是否安装。
-- 说明：本迁移必须能在「干净库」上跑通（Step 7 从零部署），因此对表不存在的情况做守卫，
-- 不能假设 legacy 表一定存在。
BEGIN;

DO $$
BEGIN
    IF to_regclass('public.sync_state') IS NOT NULL THEN
        ALTER TABLE sync_state DROP CONSTRAINT IF EXISTS sync_state_datasource_id_fkey;
        COMMENT ON COLUMN sync_state.datasource_id IS '逻辑引用 plugin-mcp 的 mcp_servers.id；插件未安装时该值为不可用，不作外键约束';
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.finding_traffic_bindings') IS NOT NULL THEN
        ALTER TABLE finding_traffic_bindings DROP CONSTRAINT IF EXISTS finding_traffic_bindings_snapshot_id_fkey;
        COMMENT ON COLUMN finding_traffic_bindings.snapshot_id IS '逻辑引用 plugin-traffic 的 traffic_evidence_snapshots.id；插件未安装时该值为不可用，不作外键约束';
    END IF;
END
$$;

INSERT INTO schema_migrations(version) VALUES ('0002_fk_direction_fix')
    ON CONFLICT (version) DO NOTHING;

COMMIT;
