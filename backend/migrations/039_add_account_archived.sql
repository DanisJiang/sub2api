-- Add archived field to accounts table
-- Archived accounts are excluded from scheduling and statistics but data is preserved

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;

-- Index for filtering archived accounts
CREATE INDEX IF NOT EXISTS idx_accounts_archived ON accounts(archived);
