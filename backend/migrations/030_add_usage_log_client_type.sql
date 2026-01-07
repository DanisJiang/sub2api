-- 为使用日志添加客户端类型字段
-- 用于区分 Claude Code 客户端和第三方客户端
-- 0: unknown (旧数据兼容), 1: claude_code, 2: other

ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS client_type SMALLINT DEFAULT 0;
