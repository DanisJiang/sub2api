-- 031_create_announcements.sql
-- Create announcements table for admin bulletin board feature.

CREATE TABLE IF NOT EXISTS announcements (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(200) NOT NULL,
    content TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    priority INTEGER NOT NULL DEFAULT 0 CHECK (priority >= 0 AND priority <= 100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_announcements_enabled ON announcements(enabled);
CREATE INDEX IF NOT EXISTS idx_announcements_priority ON announcements(priority);
CREATE INDEX IF NOT EXISTS idx_announcements_created_at ON announcements(created_at);

COMMENT ON TABLE announcements IS 'Admin announcements displayed to all users';
COMMENT ON COLUMN announcements.title IS 'Announcement title (max 200 chars)';
COMMENT ON COLUMN announcements.content IS 'Announcement content (max 10000 chars)';
COMMENT ON COLUMN announcements.enabled IS 'Whether the announcement is visible to users';
COMMENT ON COLUMN announcements.priority IS 'Display priority (0-100, higher = more prominent)';
