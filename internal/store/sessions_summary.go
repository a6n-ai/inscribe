package store

// UnsummarizedSessions returns ended sessions, ended at or after from, that
// asked something and whose summary memory is missing or older than the
// session's last event (a resumed session), oldest first.
func (s *Store) UnsummarizedSessions(from int64, limit int) ([]string, error) {
	rows, err := s.db.Query(`SELECT s.id FROM sessions s
		WHERE s.ended IS NOT NULL AND s.ended >= ?
		AND EXISTS (SELECT 1 FROM events e WHERE e.session = s.id AND e.kind = 'prompt' AND coalesce(e.prompt_text, '') != '')
		AND NOT EXISTS (SELECT 1 FROM memories m WHERE m.type = 'session' AND m.key = 'session|' || s.id AND m.last_seen >= s.last_event)
		ORDER BY s.ended LIMIT ?`, from, limit)
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
