-- Add usage limit fields to api_keys table
ALTER TABLE api_keys ADD COLUMN usage_limit DOUBLE PRECISION NULL;
ALTER TABLE api_keys ADD COLUMN total_usage DOUBLE PRECISION NOT NULL DEFAULT 0;
