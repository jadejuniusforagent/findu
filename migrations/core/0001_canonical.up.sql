-- 0001 canonical 五实体（Step 2）
-- 只新增，不改任何现有表。现有 conversations / messages 等继续工作。
BEGIN;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     text PRIMARY KEY,
    applied_at  timestamptz NOT NULL DEFAULT now()
);

-- 1 会话
CREATE TABLE IF NOT EXISTS canonical_session (
    session_id  text PRIMARY KEY,
    project_id  text,
    adapter     text NOT NULL,
    title       text,
    status      text NOT NULL DEFAULT 'active',
    meta        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 2 回合：一个 turn = 一次用户输入到 agent 停止
CREATE TABLE IF NOT EXISTS canonical_turn (
    turn_id     text PRIMARY KEY,
    session_id  text NOT NULL REFERENCES canonical_session(session_id) ON DELETE CASCADE,
    role        text NOT NULL,
    status      text NOT NULL DEFAULT 'open',
    seq         integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_canonical_turn_session ON canonical_turn(session_id, seq);

-- 3 消息
CREATE TABLE IF NOT EXISTS canonical_message (
    message_id     text PRIMARY KEY,
    turn_id        text NOT NULL REFERENCES canonical_turn(turn_id) ON DELETE CASCADE,
    role           text NOT NULL,
    content        text,
    content_parts  jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_canonical_message_turn ON canonical_message(turn_id);

-- 4 工具调用
CREATE TABLE IF NOT EXISTS canonical_tool_call (
    call_id      text PRIMARY KEY,
    turn_id      text NOT NULL REFERENCES canonical_turn(turn_id) ON DELETE CASCADE,
    tool_name    text NOT NULL,
    input        jsonb NOT NULL DEFAULT '{}'::jsonb,
    output       jsonb,
    status       text NOT NULL DEFAULT 'running',
    error        text,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    duration_ms  integer
);
CREATE INDEX IF NOT EXISTS idx_canonical_tool_call_turn ON canonical_tool_call(turn_id);
CREATE INDEX IF NOT EXISTS idx_canonical_tool_call_name ON canonical_tool_call(tool_name);

-- 5 产物
CREATE TABLE IF NOT EXISTS canonical_artifact (
    artifact_id  text PRIMARY KEY,
    session_id   text REFERENCES canonical_session(session_id) ON DELETE CASCADE,
    turn_id      text,
    kind         text NOT NULL,
    path         text NOT NULL,
    size         bigint,
    sha256       text,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_canonical_artifact_session ON canonical_artifact(session_id);

INSERT INTO schema_migrations(version) VALUES ('0001_canonical')
    ON CONFLICT (version) DO NOTHING;

COMMIT;
