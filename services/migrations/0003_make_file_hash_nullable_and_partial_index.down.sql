-- 1) Drop our new partial index
DROP INDEX IF EXISTS files_unique_hash_on_originals;

-- 2) Re-add the old unique constraint on file_hash
ALTER TABLE files
    ADD CONSTRAINT files_file_hash_key UNIQUE (file_hash);

-- 3) Restore NOT NULL on file_hash
ALTER TABLE files
    ALTER COLUMN file_hash SET NOT NULL;
