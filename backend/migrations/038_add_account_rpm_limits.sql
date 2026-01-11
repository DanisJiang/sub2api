-- Migration: Add RPM/30m limit configuration fields to accounts table
-- These fields allow per-account rate limiting configuration for Anthropic accounts

-- Add max_rpm column (requests per minute, 0 = use default)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS max_rpm INTEGER NOT NULL DEFAULT 0;

-- Add max_30m_requests column (max requests in 30 minutes, 0 = no limit)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS max_30m_requests INTEGER NOT NULL DEFAULT 0;

-- Add rate_limit_cooldown_minutes column (cooldown after hitting 30m limit, 0 = no cooldown)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS rate_limit_cooldown_minutes INTEGER NOT NULL DEFAULT 0;
