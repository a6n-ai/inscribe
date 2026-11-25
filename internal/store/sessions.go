package store

// OpenSession is a session with no end time yet.
type OpenSession struct {
	ID, Agent string
	LastEvent int64
}

// OpenSessions returns every session with no end time, newest first.
func (s *Store) OpenSessions() ([]OpenSession, error) {
	rows, err := s.db.Query(`SELECT id, agent, last_event FROM sessions WHERE ended IS NULL ORDER BY last_event DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OpenSession
	for rows.Next() {
		var o OpenSession
		if err := rows.Scan(&o.ID, &o.Agent, &o.LastEvent); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CloseSessions ends sessions whose agent stopped without a SessionEnd event,
// at their last event. Ended sessions keep their end time.
func (t *Tx) CloseSessions(ids []string) error {
	for _, id := range ids {
		if _, err := t.tx.Exec(`UPDATE sessions SET ended = last_event WHERE id = ? AND ended IS NULL`, id); err != nil {
			return err
		}
	}
	return nil
}

// IngestTarget is an ended session whose transcript has usage to read.
type IngestTarget struct {
	Session, Path string
	LastEvent     int64
}

// SessionsToIngest returns ended sessions of agent that were never ingested or
// had events after their last ingest, newest first. A lone SessionEnd (from a
// CLI subcommand such as `claude mcp get`) has no transcript, so it is skipped.
func (s *Store) SessionsToIngest(agent string, limit int) ([]IngestTarget, error) {
	rows, err := s.db.Query(`SELECT id, transcript_path, last_event FROM sessions
		WHERE agent = ? AND ended IS NOT NULL AND transcript_path IS NOT NULL AND event_count > 1
		AND (usage_ingested_at IS NULL OR last_event > usage_ingested_at)
		ORDER BY last_event DESC LIMIT ?`, agent, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []IngestTarget
	for rows.Next() {
		var t IngestTarget
		if err := rows.Scan(&t.Session, &t.Path, &t.LastEvent); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SessionRow is one session as the dashboard lists it.
type SessionRow struct {
	ID, Agent, Project      string
	Started, LastEvent      int64
	Ended                   int64 // 0 while open
	Events, Captured, Shown int
	Tokens                  int
}

const sessionRowSelect = `SELECT s.id, s.agent, s.project, s.started, s.last_event, COALESCE(s.ended, 0), s.event_count,
	(SELECT COUNT(DISTINCT me.memory_id) FROM events e JOIN memory_evidence me ON me.event_id = e.id WHERE e.session = s.id),
	(SELECT COALESCE(SUM(i.line_count), 0) FROM injections i WHERE i.session = s.id),
	(SELECT COALESCE(SUM(i.est_tokens), 0) FROM injections i WHERE i.session = s.id)
	FROM sessions s`

func scanSessionRow(r rowScanner) (SessionRow, error) {
	var o SessionRow
	err := r.Scan(&o.ID, &o.Agent, &o.Project, &o.Started, &o.LastEvent, &o.Ended, &o.Events, &o.Captured, &o.Shown, &o.Tokens)
	return o, err
}

// SessionRows returns up to limit sessions, newest activity first, with the
// memories their events are evidence for and the lines injected into them.
func (s *Store) SessionRows(limit int) ([]SessionRow, error) {
	rows, err := s.db.Query(sessionRowSelect+` ORDER BY s.last_event DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SessionRow
	for rows.Next() {
		r, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SessionByID returns one session's row, or sql.ErrNoRows.
func (s *Store) SessionByID(id string) (SessionRow, error) {
	return scanSessionRow(s.db.QueryRow(sessionRowSelect+` WHERE s.id = ?`, id))
}

// SessionCaptured returns the ids of memories that session's events are
// evidence for, in the order the session first supported them.
func (s *Store) SessionCaptured(session string) ([]string, error) {
	rows, err := s.db.Query(`SELECT me.memory_id FROM events e JOIN memory_evidence me ON me.event_id = e.id
		WHERE e.session = ? GROUP BY me.memory_id ORDER BY MIN(e.ts)`, session)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
