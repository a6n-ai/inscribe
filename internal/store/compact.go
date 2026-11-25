package store

import "context"

// Compact gives freed pages back to the file system. The first call switches
// the database to incremental auto-vacuum, which takes one full VACUUM; later
// calls release free pages without rewriting the file.
func (s *Store) Compact(ctx context.Context) (vacuumed bool, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	var mode int
	if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return false, err
	}
	const incremental = 2
	if mode != incremental {
		if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return false, err
		}
		_, err := conn.ExecContext(ctx, `VACUUM`)
		return err == nil, err
	}
	_, err = conn.ExecContext(ctx, `PRAGMA incremental_vacuum`)
	return false, err
}
