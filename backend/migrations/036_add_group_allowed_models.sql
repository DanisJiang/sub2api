-- 添加分组模型白名单字段
-- 空数组表示允许所有模型

ALTER TABLE groups ADD COLUMN IF NOT EXISTS allowed_models jsonb DEFAULT '[]'::jsonb;

-- 添加注释
COMMENT ON COLUMN groups.allowed_models IS '模型白名单，空数组表示允许所有模型';
