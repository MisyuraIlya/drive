CREATE TABLE IF NOT EXISTS chunks (
                                      id SERIAL PRIMARY KEY,
                                      user_id TEXT NOT NULL,
                                      file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_hash TEXT NOT NULL,
    index INTEGER NOT NULL,
    UNIQUE (file_id, index)
    );