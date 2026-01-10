-- Add model_mapping column to groups table
-- This allows mapping request model names to different model names sent to upstream
-- Example: {"claude-opus-4-5": "claude-opus-4-5-20251101"}

ALTER TABLE groups ADD COLUMN IF NOT EXISTS model_mapping jsonb DEFAULT '{}'::jsonb;
COMMENT ON COLUMN groups.model_mapping IS '模型名称映射，key 为请求模型，value 为实际发送模型';
