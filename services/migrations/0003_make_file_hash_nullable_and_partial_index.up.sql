-- 1) Allow NULL in file_hash
ALTER TABLE files
    ALTER COLUMN file_hash DROP NOT NULL;

-- 2) Drop the old UNIQUE constraint on file_hash
ALTER TABLE files
DROP CONSTRAINT IF EXISTS files_file_hash_key;

-- 3) Create a partial unique index: only originals (parent_file_id IS NULL) must have a unique hash
CREATE UNIQUE INDEX files_unique_hash_on_originals
    ON files(file_hash)
    WHERE parent_file_id IS NULL;
