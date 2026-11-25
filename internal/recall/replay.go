package recall

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/a6n-ai/inscribe/internal/extract"
	"github.com/a6n-ai/inscribe/internal/store"
	"github.com/a6n-ai/inscribe/model"
)

// Replay rates command lines that session and subagent starts showed, since
// those hooks write no outcome rows. A session that later ran the remembered
// command and succeeded rates it helpful. One that ran the same kind of command
// (another `cargo test`) but never this one, or ran it and failed, rates it not
// helpful. Other lines, and sessions that ran neither, stay unrated. A memory is
// rated once per session, at its first showing there, however often subagents
// repeated it. The ratings never train the score model.
func Replay(ctx context.Context, st *store.Store, w store.Window, now time.Time) (int, error) {
	injs, err := st.InjectionsIn(w)
	if err != nil {
		return 0, err
	}
	done, err := st.ReplayRated()
	if err != nil {
		return 0, err
	}
	type rating struct {
		memory, session, digest string
		label                   int
	}
	var out []rating
	mems := map[string]*model.Memory{}
	runs := map[string][]run{}
	for _, in := range injs { // oldest first, so the first showing wins
		if in.Holdout || (in.Hook != "SessionStart" && in.Hook != "SubagentStart") {
			continue
		}
		for _, id := range in.MemoryIDs {
			if done[in.Session+"\x00"+id] {
				continue
			}
			m, seen := mems[id]
			if !seen {
				if m, err = st.GetMemory(id); err != nil {
					m = nil
				}
				mems[id] = m
			}
			if m == nil || m.Type != model.Command {
				continue
			}
			rs, ok := runs[in.Session]
			if !ok {
				evs, err := st.EventsIn(in.Session, 0, math.MaxInt64)
				if err != nil {
					return 0, err
				}
				rs = sessionRuns(evs)
				runs[in.Session] = rs
			}
			done[in.Session+"\x00"+id] = true
			if label, ok := replayLabel(*m, in.TS, rs); ok {
				out = append(out, rating{id, in.Session, "inj:" + strconv.FormatInt(in.ID, 10), label})
			}
		}
	}
	if len(out) == 0 {
		return 0, nil
	}
	err = st.Write(ctx, func(tx *store.Tx) error {
		for _, r := range out {
			if err := tx.InsertRating(r.memory, r.session, r.digest, store.ReasonReplay, r.label, now.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	return len(out), err
}

// run is one command a session ran, parsed once.
type run struct {
	ts        int64
	core, key string
	ok        bool
}

func sessionRuns(evs []model.Event) []run {
	var rs []run
	for _, e := range evs {
		if e.Kind != model.CommandResult {
			continue
		}
		cmd := payloadString(e, "command")
		rs = append(rs, run{e.TS, extract.CoreCommand(cmd, e.Project), extract.CommandKey(cmd), succeeded(e)})
	}
	return rs
}

// replayLabel rates command memory m from the commands a session ran after shown.
func replayLabel(m model.Memory, shown int64, rs []run) (label int, ok bool) {
	_, want, _ := strings.Cut(m.Text, ": ")
	sameKey := false
	for _, r := range rs {
		if r.ts < shown {
			continue
		}
		if r.core == want && r.ok {
			return 1, true
		}
		if r.key == m.Key {
			sameKey = true
		}
	}
	return 0, sameKey
}
