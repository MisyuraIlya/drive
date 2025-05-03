CREATE TABLE files (
                       id              SERIAL PRIMARY KEY,
                       name            TEXT NOT NULL,
                       user_id         TEXT NOT NULL,
                       parent_file_id  INT REFERENCES files(id),
                       type            TEXT NOT NULL,
                       ready           BOOLEAN NOT NULL DEFAULT false,
                       file_hash       TEXT NOT NULL UNIQUE
);
