package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/a6n-ai/inscribe/internal/trajectory"
	"github.com/a6n-ai/inscribe/model"
)

func addTrajectory(t *testing.T, st *Store, project string, ts int64, json string) int64 {
	t.Helper()
	var id int64
	err := st.Write(context.Background(), func(tx *Tx) error {
		e := &model.Event{TS: ts, Agent: "claude-code", Session: "s-" + project, Project: project, Kind: model.Prompt, Digest: fmt.Sprint(ts, project)}
		if _, err := tx.InsertEvent(e); err != nil {
			return err
		}
		if err := tx.InsertTrajectory(e, []trajectory.Record{{Role: "user", TS: ts, JSON: []byte(json), Digest: fmt.Sprint("d", ts, project)}}); err != nil {
			return err
		}
		id = e.ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTrajectorySearchIndexedAndPlain(t *testing.T) {
	st, _ := openTemp(t)
	addTrajectory(t, st, "/a", 10, `{"text":"the launchd label for sorter"}`)
	addTrajectory(t, st, "/b", 11, `{"text":"the launchd label elsewhere"}`)
	var typ string
	if err := st.db.QueryRow(`SELECT typeof(record) FROM trajectory_records LIMIT 1`).Scan(&typ); err != nil || typ != "blob" {
		t.Fatalf("new record stored as %q (%v), want a gzip blob", typ, err)
	}
	// A row written before migration 0010: plain text and not indexed.
	if _, err := st.db.Exec(`INSERT INTO trajectory_records(event_id, session, project, agent, role, ts, record, digest)
		SELECT event_id, session, project, agent, role, 12, '{"text":"old launchd note"}', 'plain' FROM trajectory_records WHERE project = '/a'`); err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchTrajectory("/a", []string{"launchd"}, 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits = %+v, %v; want the indexed and the plain /a rows, not /b", hits, err)
	}
	if hits[0].Record != `{"text":"old launchd note"}` || hits[1].Record != `{"text":"the launchd label for sorter"}` {
		t.Fatalf("records = %q, %q; want newest first and unzipped", hits[0].Record, hits[1].Record)
	}
	if hits, _ := st.SearchTrajectory("/a", []string{"nothing"}, 10); len(hits) != 0 {
		t.Fatalf("unmatched term found %+v", hits)
	}
	var n int
	if err := st.Write(context.Background(), func(tx *Tx) error { n, err = tx.IndexTrajectory(100); return err }); err != nil || n != 1 {
		t.Fatalf("IndexTrajectory = %d, %v; want the one plain row", n, err)
	}
	if hits, _ := st.SearchTrajectory("/a", []string{"launchd"}, 10); len(hits) != 2 {
		t.Fatalf("after indexing, hits = %+v", hits)
	}
	recs, err := st.TrajectoryJSON("s-/a")
	if err != nil || len(recs) != 2 || recs[0] != `{"text":"the launchd label for sorter"}` {
		t.Fatalf("TrajectoryJSON = %q, %v", recs, err)
	}
	// Deleting rows, here by age, takes them out of the index too.
	if err := st.Write(context.Background(), func(tx *Tx) error { n, err = tx.DeleteTrajectoryBefore(12, 100); return err }); err != nil || n != 2 {
		t.Fatalf("DeleteTrajectoryBefore = %d, %v", n, err)
	}
	if hits, _ := st.SearchTrajectory("/b", []string{"launchd"}, 10); len(hits) != 0 {
		t.Fatalf("deleted row still found: %+v", hits)
	}
	var left int
	if err := st.db.QueryRow(`SELECT count(*) FROM trajectory_fts WHERE trajectory_fts MATCH 'launchd'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("index rows left = %d, %v; want only the undeleted /a row", left, err)
	}
}

func TestSearchFTSFiltersByTags(t *testing.T) {
	st, _ := openTemp(t)
	var noteID string
	err := st.Write(context.Background(), func(tx *Tx) error {
		for _, m := range []model.Memory{
			{Type: model.Note, Status: model.Active, Key: "n", Text: "deploy with wrangler"},
			{Type: model.Cochange, Status: model.Candidate, Key: "c", Text: "deploy files change together"},
			{Type: model.SkillUse, Status: model.Active, Key: "s", Text: "deploy skill"},
		} {
			m.ScopeKind, m.Scope, m.Confidence, m.FirstSeen, m.LastSeen = model.ProjectScope, "/p", 0.5, 1, 1
			got, err := tx.UpsertMemory(&m, nil, 0)
			if err != nil {
				return err
			}
			if m.Type == model.Note {
				noteID = got.ID
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	search := func(f ListFilter) int {
		t.Helper()
		ms, _, err := st.SearchFTS([]string{"deploy"}, f, 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(ms)
	}
	if n := search(ListFilter{}); n != 3 {
		t.Fatalf("no filter = %d, want 3", n)
	}
	if n := search(ListFilter{Statuses: []model.Status{model.Active}}); n != 2 {
		t.Fatalf("active = %d, want 2", n)
	}
	if n := search(ListFilter{Types: []model.MemoryType{model.SkillUse}}); n != 1 {
		t.Fatalf("skill_use (underscore type) = %d, want 1", n)
	}
	if err := st.Write(context.Background(), func(tx *Tx) error { return tx.SetStatus(noteID, model.Forgotten) }); err != nil {
		t.Fatal(err)
	}
	if n := search(ListFilter{Types: []model.MemoryType{model.Note}, Statuses: []model.Status{model.Active}}); n != 0 {
		t.Fatalf("a forgotten note still matched as active: %d", n)
	}
}

func TestTrimSessionSummaries(t *testing.T) {
	st, _ := openTemp(t)
	err := st.Write(context.Background(), func(tx *Tx) error {
		for i := range 5 {
			for _, p := range []string{"/a", "/b"} {
				m := &model.Memory{Type: model.Session, ScopeKind: model.ProjectScope, Scope: p, Key: fmt.Sprint("session|", p, i),
					Text: "s", Confidence: 0.9, Status: model.Active, FirstSeen: int64(i), LastSeen: int64(i)}
				if _, err := tx.UpsertMemory(m, nil, 0); err != nil {
					return err
				}
			}
		}
		n, err := tx.TrimSessionSummaries(3)
		if err == nil && n != 4 {
			err = fmt.Errorf("trimmed %d, want 4", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ms, err := st.ListMemories(ListFilter{Types: []model.MemoryType{model.Session}, Project: "/a"})
	if err != nil || len(ms) != 3 || ms[0].LastSeen != 4 || ms[2].LastSeen != 2 {
		t.Fatalf("kept %+v, %v; want the newest 3 of /a", ms, err)
	}
}

func TestCompactSwitchesToIncremental(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	if vac, err := st.Compact(ctx); err != nil || !vac {
		t.Fatalf("first compact = %v, %v; want one VACUUM", vac, err)
	}
	var mode int
	if err := st.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != 2 {
		t.Fatalf("auto_vacuum = %d, %v; want 2", mode, err)
	}
	if vac, err := st.Compact(ctx); err != nil || vac {
		t.Fatalf("second compact = %v, %v; want incremental only", vac, err)
	}
}
