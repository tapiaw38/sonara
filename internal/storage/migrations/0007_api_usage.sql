CREATE TABLE api_usage (
    id INTEGER PRIMARY KEY,
    at_ms INTEGER NOT NULL,
    provider TEXT NOT NULL,
    function TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    audio_input_tokens INTEGER NOT NULL,
    audio_output_tokens INTEGER NOT NULL,
    audio_seconds REAL NOT NULL,
    characters INTEGER NOT NULL,
    tokens_known INTEGER NOT NULL,
    ok INTEGER NOT NULL
);
CREATE INDEX api_usage_at ON api_usage(at_ms);
