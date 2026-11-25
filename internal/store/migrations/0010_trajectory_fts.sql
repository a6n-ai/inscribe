-- Trajectory search through a full-text index instead of a LIKE scan of every
-- record. The index keeps no copy of the text (contentless), each row carries a
-- project token so a search filters inside the index, and a deleted record
-- (directly or by its event's cascade) leaves the index with it.
-- Records move to gzip as they are indexed; rows still stored as plain text are
-- the ones not indexed yet, and the partial index finds them for the fallback.
CREATE VIRTUAL TABLE trajectory_fts USING fts5(terms, content='', contentless_delete=1);
CREATE TRIGGER trajectory_fts_ad AFTER DELETE ON trajectory_records BEGIN
  DELETE FROM trajectory_fts WHERE rowid = old.id;
END;
CREATE INDEX trajectory_plain ON trajectory_records(project, id) WHERE typeof(record) = 'text';
CREATE INDEX trajectory_ts ON trajectory_records(ts);
