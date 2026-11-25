package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/a6n-ai/inscribe"
	"github.com/a6n-ai/inscribe/internal/store"
	"github.com/a6n-ai/inscribe/model"
)

func TestServeSessions(t *testing.T) {
	home := t.TempDir()
	h, err := inscribe.Open(inscribe.Options{Home: home, Caller: "inscribe"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	for _, e := range []model.Event{
		{TS: now, Agent: "claude-code", Session: "s-open", Project: "/src/quokka", Kind: model.Prompt, PromptText: "build it"},
		{TS: now - 2*time.Hour.Milliseconds(), Agent: "claude-code", Session: "s-done", Project: "/src/wombat", Kind: model.SessionEnd},
	} {
		if err := h.Capture(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	_ = h.Close()

	srv := newServeHandler(home, "127.0.0.1", 7331)
	for target, want := range map[string]string{
		"/sessions":        "1 open · 0 idle · 1 closed",
		"/sessions/s-open": "Memories captured",
		"/sessions/s-done": "wombat",
	} {
		rec := serveReq(srv, "GET", target, "", testHost, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("GET %s = %d, want %q in:\n%s", target, rec.Code, want, rec.Body)
		}
	}
	if rec := serveReq(srv, "GET", "/sessions/nope", "", testHost, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", rec.Code)
	}
}

func TestSessionState(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		r    store.SessionRow
		want string
	}{
		{store.SessionRow{LastEvent: now.UnixMilli()}, "open"},
		{store.SessionRow{LastEvent: now.Add(-time.Hour).UnixMilli()}, "idle"},
		{store.SessionRow{LastEvent: now.UnixMilli(), Ended: now.UnixMilli()}, "closed"},
	} {
		if got := sessionState(c.r, now); got != c.want {
			t.Fatalf("sessionState(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestLaunchdPlistEscapesAndRestartsOnlyOnFailure(t *testing.T) {
	p := launchdPlist("/opt/a&b/inscribe", "127.0.0.1:7331", "/Users/x/.inscribe")
	for _, want := range []string{"/opt/a&amp;b/inscribe", "<key>SuccessfulExit</key><false/>", "/Users/x/.inscribe/serve.log"} {
		if !strings.Contains(p, want) {
			t.Fatalf("plist lacks %q:\n%s", want, p)
		}
	}
}
