package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeCF struct {
	mu      sync.Mutex
	created int
	deleted []string
	server  *httptest.Server
	life    time.Duration
	silent  bool
}

func (f *fakeCF) visit(ray string) []byte {
	line, _ := json.Marshal(map[string]any{"msg": "visit", "path": "/", "cf": map[string]string{"cf-ray": ray}})
	other, _ := json.Marshal(map[string]any{"msg": "other", "ray": "other-shared"})
	msg, _ := json.Marshal(map[string]any{
		"logs": []any{map[string]any{"message": []any{string(line)}}, map[string]any{"message": []any{"not a visit"}},
			map[string]any{"message": []any{string(other)}}},
		"event": map[string]any{"request": map[string]any{"url": "https://example.com/"}},
	})
	return msg
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tails"):
		f.mu.Lock()
		f.created++
		n := f.created
		f.mu.Unlock()
		url := "ws" + strings.TrimPrefix(f.server.URL, "http") + fmt.Sprintf("/ws/%d", n)
		fmt.Fprintf(w, `{"result":{"id":"t%d","url":%q,"expires_at":%q}}`, n, url, time.Now().Add(cmp.Or(f.life, 500*time.Millisecond)).Format(time.RFC3339Nano))
	case r.Method == http.MethodDelete:
		f.mu.Lock()
		f.deleted = append(f.deleted, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		f.mu.Unlock()
	case strings.HasPrefix(r.URL.Path, "/ws/"):
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"trace-v1"}})
		if err != nil {
			return
		}
		ctx := r.Context()
		if _, hello, err := conn.Read(ctx); err != nil || string(hello) != `{"debug":false}` {
			conn.Close(websocket.StatusPolicyViolation, "no hello")
			return
		}
		conn.Write(ctx, websocket.MessageText, f.visit("shared"))
		conn.Write(ctx, websocket.MessageText, f.visit("ray-"+strings.TrimPrefix(r.URL.Path, "/ws/")))
		conn.Write(ctx, websocket.MessageText, []byte(`{"event":{"type":"overload","message":"sampling"},"logs":[]}`))
		if !f.silent {
			ctx = conn.CloseRead(ctx)
		}
		<-ctx.Done()
	default:
		http.NotFound(w, r)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestTailRenewsBeforeExpiryAndWritesEachVisitOnce(t *testing.T) {
	f := &fakeCF{}
	f.server = httptest.NewServer(f)
	defer f.server.Close()
	cfAPI = f.server.URL

	var out syncBuffer
	tl := &tailer{worker: "citrine-shield", token: "x", account: "acct", out: &out, seen: map[string]time.Time{}, lines: make(chan []byte, 64)}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := tl.run(ctx); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	created, deleted := f.created, len(f.deleted)
	f.mu.Unlock()
	if created < 3 {
		t.Fatalf("made %d tails in 1.5s of 0.5s ones; it isn't renewing", created)
	}
	if deleted != created {
		t.Fatalf("made %d tails but deleted %d", created, deleted)
	}
	got := out.String()
	if n := strings.Count(got, `"shared"`); n != 1 {
		t.Fatalf("the same ray was written %d times:\n%s", n, got)
	}
	if strings.Contains(got, "not a visit") || strings.Contains(got, "sampling") {
		t.Fatalf("wrote something that isn't a visit:\n%s", got)
	}
	if strings.Contains(got, "other-shared") {
		t.Fatalf("wrote another message as a visit:\n%s", got)
	}
	for i := 1; i <= created-1; i++ {
		if !strings.Contains(got, fmt.Sprintf(`"ray-%d"`, i)) {
			t.Fatalf("missed tail %d's visit:\n%s", i, got)
		}
	}
}

func TestTailReopensWhenPingsGoUnanswered(t *testing.T) {
	defer func(every, wait time.Duration) { pingEvery, pongWait = every, wait }(pingEvery, pongWait)
	pingEvery, pongWait = 20*time.Millisecond, 20*time.Millisecond
	for _, silent := range []bool{false, true} {
		f := &fakeCF{life: time.Hour, silent: silent}
		f.server = httptest.NewServer(f)
		cfAPI = f.server.URL
		tl := &tailer{worker: "citrine-shield", token: "x", account: "acct", out: &syncBuffer{}, seen: map[string]time.Time{}, lines: make(chan []byte, 64)}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if err := tl.run(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		f.mu.Lock()
		created, deleted := f.created, len(f.deleted)
		f.mu.Unlock()
		f.server.Close()
		switch {
		case silent && created < 3:
			t.Fatalf("made %d tails in 0.5s of a peer that never answers a ping", created)
		case !silent && created != 1:
			t.Fatalf("made %d tails of one that answers its pings", created)
		case deleted != created:
			t.Fatalf("made %d tails but deleted %d", created, deleted)
		}
	}
}

func TestTailStopsOnARefusedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"success":false}`, http.StatusForbidden)
	}))
	defer srv.Close()
	cfAPI = srv.URL
	tl := &tailer{worker: "citrine-shield", token: "x", account: "acct", out: &syncBuffer{}, seen: map[string]time.Time{}, lines: make(chan []byte, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tl.run(ctx); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want a 403 error, got %v", err)
	}
}

func TestTheDashboardReadsTheWorkersLine(t *testing.T) {
	line := `{"msg":"visit","time":"2026-11-07T21:25:49.607Z","status":200,"method":"GET","host":"example.com","path":"/a","userAgent":"UA","referer":"","cf":{"cf-connecting-ip":"10.0.0.1","cf-ipcountry":"US","cf-ray":"r1"}}`
	v := parseVisits(strings.NewReader(line + "\n" + line + "\n"))
	if v.requests != 2 || len(v.visitors) != 1 {
		t.Fatalf("got %+v", v)
	}
	if vis := v.visitors[0]; vis.ip != "10.0.0.1" || vis.country != "US" || vis.agent != "UA" || vis.status != 200 {
		t.Fatalf("got %+v", vis)
	}
}

func TestAGapBetweenCloudflareAndTheLogShows(t *testing.T) {
	now := time.Now()
	logged := visits{visitors: []visitor{{last: now.Add(-time.Hour)}, {last: now.Add(-2 * time.Hour)}}}
	cases := []struct {
		name   string
		served time.Time
		v      visits
		missed bool
	}{
		{"served after the newest logged visit", now.Add(-30 * time.Minute), logged, true},
		{"served just around it", now.Add(-55 * time.Minute), logged, false},
		{"nothing served", time.Time{}, logged, false},
		{"served, with an empty log", now.Add(-time.Hour), visits{}, true},
	}
	for _, c := range cases {
		if _, missed := missedSince(traffic{lastServed: c.served}, c.v); missed != c.missed {
			t.Errorf("%s: missed=%v, want %v", c.name, missed, c.missed)
		}
	}
}

func TestTrafficKnowsWhenItLastServed(t *testing.T) {
	b := []byte(`{"data":{"viewer":{"zones":[{"hist":[
		{"count":3,"dimensions":{"t":"2026-11-07T10:00:00Z"}},
		{"count":1,"dimensions":{"t":"2026-11-07T11:30:00Z"}}],
		"fetched":[{"dimensions":{"t":"2026-11-07T11:15:42Z"}}]}]}}}`)
	since, _ := time.Parse(time.RFC3339, "2026-11-07T00:00:00Z")
	tr, err := parseTraffic(b, since, 24*time.Hour, 48)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := time.Parse(time.RFC3339, "2026-11-07T11:15:42Z"); !tr.lastServed.Equal(want) {
		t.Fatalf("lastServed = %v", tr.lastServed)
	}
}
