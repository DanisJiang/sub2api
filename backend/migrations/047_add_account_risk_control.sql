-- 添加账号风控对抗开关
-- 启用后会在请求前检查风险分数，自动调节请求间隔（仅限Anthropic账号）

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS risk_control_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_accounts_risk_control_enabled ON accounts(risk_control_enabled);

COMMENT ON COLUMN accounts.risk_control_enabled IS '启用风控对抗，自动调节请求间隔降低被封风险（仅限Anthropic账号）';
