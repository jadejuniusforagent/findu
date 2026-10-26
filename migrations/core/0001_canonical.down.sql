-- 0001 down：回滚 canonical 五实体
BEGIN;
DROP TABLE IF EXISTS canonical_artifact;
DROP TABLE IF EXISTS canonical_tool_call;
DROP TABLE IF EXISTS canonical_message;
DROP TABLE IF EXISTS canonical_turn;
DROP TABLE IF EXISTS canonical_session;
DELETE FROM schema_migrations WHERE version = '0001_canonical';
COMMIT;
