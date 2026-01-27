-- Sync total_usage from usage_logs for existing API keys
-- This ensures historical usage data is correctly reflected in the total_usage field
-- This migration is idempotent and safe to run multiple times

UPDATE api_keys
SET total_usage = COALESCE(
    (SELECT SUM(actual_cost) FROM usage_logs WHERE usage_logs.api_key_id = api_keys.id),
    0
);
