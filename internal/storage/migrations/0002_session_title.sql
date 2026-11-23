-- Sessions are named by the model after their first exchange, so the list reads
-- as topics instead of timestamps.
ALTER TABLE sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';
