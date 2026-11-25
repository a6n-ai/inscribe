-- Memory search filters by type and status inside the full-text index, so a
-- query ranks only the rows it can return (active ones, or the types a
-- cross-project pass reads) instead of every row that shares a word. The index
-- is contentless and keeps its own tags column; triggers keep it in step.
DROP TRIGGER memories_ai;
DROP TRIGGER memories_ad;
DROP TRIGGER memories_au;
DROP TABLE memories_fts;
CREATE VIRTUAL TABLE memories_fts USING fts5(
  terms, key, tags,
  content='', contentless_delete=1,
  tokenize='unicode61 remove_diacritics 2'
);
INSERT INTO memories_fts(rowid, terms, key, tags)
  SELECT rowid, terms, key, 'ty' || replace(type, '_', '') || ' st' || status FROM memories;
CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
  INSERT INTO memories_fts(rowid, terms, key, tags)
    VALUES (new.rowid, new.terms, new.key, 'ty' || replace(new.type, '_', '') || ' st' || new.status);
END;
CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
  DELETE FROM memories_fts WHERE rowid = old.rowid;
END;
CREATE TRIGGER memories_au AFTER UPDATE OF terms, key, type, status ON memories BEGIN
  DELETE FROM memories_fts WHERE rowid = old.rowid;
  INSERT INTO memories_fts(rowid, terms, key, tags)
    VALUES (new.rowid, new.terms, new.key, 'ty' || replace(new.type, '_', '') || ' st' || new.status);
END;
