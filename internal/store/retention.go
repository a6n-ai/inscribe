package store

import (
	"database/sql"
	"errors"

	"github.com/a6n-ai/inscribe/model"
)

// GetMeta returns a meta value and whether it exists.
func (s *Store) GetMeta(k string) (string, bool, error) { return getMeta(s.db, k) }

// GetMeta returns a meta value inside the transaction.
func (t *Tx) GetMeta(k string) (string, bool, error) { return getMeta(t.tx, k) }

func getMeta(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, k string) (string, bool, error) {
	var v string
	err := q.QueryRow(`SELECT v FROM meta WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetMeta stores a meta value.
func (t *Tx) SetMeta(k, v string) error {
	_, err := t.tx.Exec(`INSERT INTO meta(k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// ExpiringPrompts returns up to limit events that still hold prompt text and
// whose clock (original ts, or stored_ts) is before cutoff, oldest id first.
func (t *Tx) ExpiringPrompts(cutoff int64, byOriginal bool, limit int) ([]model.Event, error) {
	clock := "e.stored_ts"
	if byOriginal {
		clock = "e.ts"
	}
	rows, err := t.tx.Query(eventSelect+` WHERE e.prompt_text IS NOT NULL AND `+clock+` < ? ORDER BY e.id LIMIT ?`, cutoff, limit) //nolint:gosec // G202: clock is one of two constants; values are bound
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// CopyToReplay records each event's prompt text in replay_prompts. Rows already
// present are kept. It returns how many rows were added.
func (t *Tx) CopyToReplay(evs []model.Event) (int, error) {
	added := 0
	for _, e := range evs {
		res, err := t.tx.Exec(`INSERT OR IGNORE INTO replay_prompts(event_id, project, ts, prompt_text) VALUES (?, ?, ?, ?)`,
			e.ID, e.Project, e.TS, e.PromptText)
		if err != nil {
			return added, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return added, err
		}
		added += int(n)
	}
	return added, nil
}

// ClearPromptText removes the prompt text of the given events and returns how many changed.
func (t *Tx) ClearPromptText(evs []model.Event) (int, error) {
	cleared := 0
	for _, e := range evs {
		res, err := t.tx.Exec(`UPDATE events SET prompt_text = NULL WHERE id = ?`, e.ID)
		if err != nil {
			return cleared, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return cleared, err
		}
		cleared += int(n)
	}
	return cleared, nil
}

// TrimReplay keeps the newest keep replay rows by original time and returns how
// many it dropped.
func (t *Tx) TrimReplay(keep int) (int64, error) {
	res, err := t.tx.Exec(`DELETE FROM replay_prompts WHERE event_id NOT IN
		(SELECT event_id FROM replay_prompts ORDER BY ts DESC, event_id DESC LIMIT ?)`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DropReplayBefore removes replay rows whose original time is before cutoff.
func (t *Tx) DropReplayBefore(cutoff int64) (int64, error) {
	res, err := t.tx.Exec(`DELETE FROM replay_prompts WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteOldEvents deletes up to limit events older than cutoff that no active or
// superseded memory cites as evidence and whose prompt text is already cleared,
// so a prompt is always sampled before its event goes. It returns how many were deleted.
func (t *Tx) DeleteOldEvents(cutoff int64, limit int) (int64, error) {
	res, err := t.tx.Exec(`DELETE FROM events WHERE id IN (
		SELECT e.id FROM events e WHERE e.ts < ? AND e.prompt_text IS NULL AND NOT EXISTS (
			SELECT 1 FROM memory_evidence me JOIN memories m ON m.id = me.memory_id
			WHERE me.event_id = e.id AND m.status <> 'forgotten')
		ORDER BY e.id LIMIT ?)`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteStaleCandidates deletes candidate memories of type typ last seen before
// cutoff, with their evidence links, so the events they cite can expire too.
func (t *Tx) DeleteStaleCandidates(typ model.MemoryType, cutoff int64) (int64, error) {
	const pick = `SELECT id FROM memories WHERE type = ? AND status = 'candidate' AND last_seen < ?`
	if _, err := t.tx.Exec(`DELETE FROM memory_evidence WHERE memory_id IN (`+pick+`)`, string(typ), cutoff); err != nil {
		return 0, err
	}
	res, err := t.tx.Exec(`DELETE FROM memories WHERE id IN (`+pick+`)`, string(typ), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountEventsBefore returns how many events are older than cutoff.
func (t *Tx) CountEventsBefore(cutoff int64) (int, error) {
	var n int
	err := t.tx.QueryRow(`SELECT count(*) FROM events WHERE ts < ?`, cutoff).Scan(&n)
	return n, err
}

// DropOrphanEvidence removes evidence links whose event no longer exists.
func (t *Tx) DropOrphanEvidence() error {
	_, err := t.tx.Exec(`DELETE FROM memory_evidence WHERE event_id NOT IN (SELECT id FROM events)`)
	return err
}

// TrimSessionSummaries deletes session summary memories beyond the newest keep
// of each project.
func (t *Tx) TrimSessionSummaries(keep int) (int64, error) {
	const pick = `SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY scope ORDER BY last_seen DESC, id) rn
		FROM memories WHERE type = 'session') WHERE rn > ?`
	if _, err := t.tx.Exec(`DELETE FROM memory_evidence WHERE memory_id IN (`+pick+`)`, keep); err != nil {
		return 0, err
	}
	res, err := t.tx.Exec(`DELETE FROM memories WHERE id IN (`+pick+`)`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
