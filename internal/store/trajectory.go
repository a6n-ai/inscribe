package store

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/a6n-ai/inscribe/internal/text"

	"github.com/a6n-ai/inscribe/internal/trajectory"
	"github.com/a6n-ai/inscribe/model"
)

// TrajectoryHit is one stored trajectory v1 record whose JSON matched a query.
type TrajectoryHit struct {
	EventID     int64
	Role, Agent string
	Record      string
}

// InsertTrajectory stores recs for e. A digest that is already stored is skipped,
// so a replay of the same event does not duplicate the transcript.
func (t *Tx) InsertTrajectory(e *model.Event, recs []trajectory.Record) error {
	for _, r := range recs {
		packed, err := packRecord(string(r.JSON))
		if err != nil {
			return err
		}
		res, err := t.tx.Exec(`INSERT INTO trajectory_records(
			event_id, session, project, agent, role, tool_call_id, ok, ts, record, digest)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(digest) DO NOTHING`,
			e.ID, e.Session, e.Project, e.Agent, r.Role, nullable(r.ToolCallID), bit(r.OK), nullInt(r.TS), packed, r.Digest)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			continue
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := t.indexTrajectory(id, e.Project, string(r.JSON)); err != nil {
			return err
		}
	}
	return nil
}

// projectToken is the term a trajectory row's project is indexed under, so a
// search can keep to one project inside the full-text index.
func projectToken(project string) string {
	sum := sha256.Sum256([]byte(project))
	return "zproj" + hex.EncodeToString(sum[:8])
}

func (t *Tx) indexTrajectory(id int64, project, record string) error {
	terms := append([]string{projectToken(project)}, text.Terms(record)...)
	_, err := t.tx.Exec(`INSERT INTO trajectory_fts(rowid, terms) VALUES (?, ?)`, id, strings.Join(terms, " "))
	return err
}

// packRecord gzips a record. Stored as a blob, it also marks the row as indexed.
func packRecord(rec string) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(rec)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unpackRecord returns a record as text, whether it is stored gzipped or plain.
func unpackRecord(raw []byte) (string, error) {
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return string(raw), nil
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("trajectory record: %w", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("trajectory record: %w", err)
	}
	return string(out), nil
}

// IndexTrajectory gzips and indexes up to limit records still stored as plain
// text, oldest first, and reports how many it converted.
func (t *Tx) IndexTrajectory(limit int) (int, error) {
	rows, err := t.tx.Query(`SELECT id, project, record FROM trajectory_records
		WHERE typeof(record) = 'text' ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	type plain struct {
		id            int64
		project, json string
	}
	var todo []plain
	for rows.Next() {
		var p plain
		if err := rows.Scan(&p.id, &p.project, &p.json); err != nil {
			_ = rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, p := range todo {
		packed, err := packRecord(p.json)
		if err != nil {
			return 0, err
		}
		if _, err := t.tx.Exec(`UPDATE trajectory_records SET record = ? WHERE id = ?`, packed, p.id); err != nil {
			return 0, err
		}
		if err := t.indexTrajectory(p.id, p.project, p.json); err != nil {
			return 0, err
		}
	}
	return len(todo), nil
}

// DeleteTrajectoryBefore deletes up to limit records written before ts.
func (t *Tx) DeleteTrajectoryBefore(ts int64, limit int) (int, error) {
	res, err := t.tx.Exec(`DELETE FROM trajectory_records WHERE id IN
		(SELECT id FROM trajectory_records WHERE ts < ? ORDER BY ts LIMIT ?)`, ts, limit)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// TrajectoryJSON returns one session's trajectory v1 records in write order.
func (s *Store) TrajectoryJSON(session string) ([]string, error) {
	rows, err := s.db.Query(`SELECT record FROM trajectory_records WHERE session = ? ORDER BY id`, session)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		rec, err := unpackRecord(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SearchTrajectory returns project's newest records whose text holds any of
// terms: indexed records through the full-text index, and records not indexed
// yet (still plain text) through a LIKE over that small remainder.
func (s *Store) SearchTrajectory(project string, terms []string, limit int) ([]TrajectoryHit, error) {
	if project == "" || limit <= 0 {
		return nil, nil
	}
	var quoted, likes []string
	var likeArgs []any
	seen := map[string]bool{}
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		likes = append(likes, `record LIKE ? ESCAPE '\'`)
		likeArgs = append(likeArgs, "%"+escapeLike(term)+"%")
		if len(quoted) == 12 {
			break
		}
	}
	if len(quoted) == 0 {
		return nil, nil
	}
	match := projectToken(project) + " AND (" + strings.Join(quoted, " OR ") + ")"
	ids, err := s.queryIDs(`SELECT rowid FROM trajectory_fts WHERE trajectory_fts MATCH ? ORDER BY rowid DESC LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // G202: only "?" placeholders are concatenated; every value is bound
	plain, err := s.queryIDs(`SELECT id FROM trajectory_records INDEXED BY trajectory_plain
		WHERE project = ? AND typeof(record) = 'text' AND (`+strings.Join(likes, " OR ")+`) ORDER BY id DESC LIMIT ?`,
		append(append([]any{project}, likeArgs...), limit)...)
	if err != nil {
		return nil, err
	}
	ids = append(ids, plain...)
	slices.SortFunc(ids, func(a, b int64) int { return cmp.Compare(b, a) })
	ids = ids[:min(len(ids), limit)]
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT event_id, role, agent, record FROM trajectory_records WHERE id IN (`+placeholders(len(ids))+`) ORDER BY id DESC`, args...) //nolint:gosec // G202: only "?" placeholders are concatenated
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TrajectoryHit
	for rows.Next() {
		var h TrajectoryHit
		var raw []byte
		if err := rows.Scan(&h.EventID, &h.Role, &h.Agent, &raw); err != nil {
			return nil, err
		}
		if h.Record, err = unpackRecord(raw); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) queryIDs(q string, args ...any) ([]int64, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EvidencePair links a memory to an event it cites.
type EvidencePair struct {
	MemoryID string
	EventID  int64
}

// EvidencePairs returns the memory/event links for the given events.
func (s *Store) EvidencePairs(eventIDs []int64) ([]EvidencePair, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(eventIDs))
	for i, id := range eventIDs {
		args[i] = id
	}
	q := `SELECT memory_id, event_id FROM memory_evidence WHERE event_id IN (` + placeholders(len(eventIDs)) + `)` //nolint:gosec // G202: only "?" placeholders are concatenated
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EvidencePair
	for rows.Next() {
		var p EvidencePair
		if err := rows.Scan(&p.MemoryID, &p.EventID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func bit(ok *bool) any {
	if ok == nil {
		return nil
	}
	if *ok {
		return 1
	}
	return 0
}
