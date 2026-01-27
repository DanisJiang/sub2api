-- Add usage limit fields to api_keys table
-- usage_limit: Usage limit in USD, null means unlimited
-- total_usage: Total accumulated usage in USD

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS usage_limit DOUBLE PRECISION NULL;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS total_usage DOUBLE PRECISION NOT NULL DEFAULT 0;
