-- Interview-specific context belongs to its session and follows it through
-- reopen, delete, and database backup.
ALTER TABLE sessions ADD COLUMN session_profile TEXT NOT NULL DEFAULT '{}';
