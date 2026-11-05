-- 分析任务表与索引；不修改既有链上采集表。

CREATE TABLE IF NOT EXISTS analysis_tasks (
    id BIGSERIAL PRIMARY KEY,
    chain_id BIGINT NOT NULL,
    address TEXT NOT NULL,
    depth INTEGER NOT NULL DEFAULT 1 CHECK (depth BETWEEN 1 AND 5),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    result JSONB,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_analysis_tasks_created_at
    ON analysis_tasks (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_analysis_tasks_chain_address_created_at
    ON analysis_tasks (chain_id, lower(address), created_at DESC);
