package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

var cfAPI = "https://api.cloudflare.com/client/v4"

var pingEvery, pongWait = 5 * time.Second, 5 * time.Second

type tailSession struct {
	id      string
	url     string
	expires time.Time
}

type apiError struct {
	status int
	body   string
}

func (e apiError) Error() string { return fmt.Sprintf("%d from Cloudflare: %s", e.status, e.body) }

func cfCall(ctx context.Context, token, method, path string, body any, into any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfAPI+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return apiError{resp.StatusCode, string(bytes.TrimSpace(b))}
	}
	if into == nil {
		return nil
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	return json.Unmarshal(env.Result, into)
}

type tailer struct {
	worker  string
	token   string
	account string
	out     io.Writer
	seen    map[string]time.Time
	swept   time.Time
	lines   chan []byte
	closing sync.WaitGroup
}

func (t *tailer) open(ctx context.Context) (tailSession, *websocket.Conn, error) {
	var s struct {
		ID        string    `json:"id"`
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := "/accounts/" + t.account + "/workers/scripts/" + t.worker + "/tails"
	if err := cfCall(ctx, t.token, http.MethodPost, path, map[string]any{"filters": []any{}}, &s); err != nil {
		return tailSession{}, nil, err
	}
	session := tailSession{s.ID, s.URL, s.ExpiresAt}
	conn, _, err := websocket.Dial(ctx, s.URL, &websocket.DialOptions{
		Subprotocols: []string{"trace-v1"},
		HTTPHeader:   http.Header{"User-Agent": {userAgent}},
	})
	if err != nil {
		t.close(session, nil)
		return tailSession{}, nil, err
	}
	conn.SetReadLimit(1 << 22)
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"debug":false}`)); err != nil {
		t.close(session, conn)
		return tailSession{}, nil, err
	}
	return session, conn, nil
}

func (t *tailer) close(s tailSession, conn *websocket.Conn) {
	if conn != nil {
		conn.CloseNow()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := "/accounts/" + t.account + "/workers/scripts/" + t.worker + "/tails/" + s.id
	if err := cfCall(ctx, t.token, http.MethodDelete, path, nil, nil); err != nil {
		fmt.Fprintln(os.Stderr, "citrine-visits: couldn't delete", t.worker, "tail", s.id+":", err)
	}
}

func (t *tailer) read(ctx context.Context, conn *websocket.Conn, done chan<- error) {
	for {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			report(done, err)
			return
		}
		t.lines <- msg
	}
}

func (t *tailer) ping(ctx context.Context, conn *websocket.Conn, done chan<- error, every, wait time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		pong, cancel := context.WithTimeout(ctx, wait)
		err := conn.Ping(pong)
		cancel()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				report(done, err)
			}
			return
		}
	}
}

func report(done chan<- error, err error) {
	select {
	case done <- err:
	default:
	}
}

func visitLines(msg []byte) (lines [][]byte, notice string) {
	var e struct {
		Logs []struct {
			Message []json.RawMessage `json:"message"`
		} `json:"logs"`
		Event struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"event"`
	}
	if json.Unmarshal(msg, &e) != nil {
		return nil, ""
	}
	if e.Event.Type != "" && e.Event.Message != "" {
		notice = e.Event.Type + ": " + e.Event.Message
	}
	for _, l := range e.Logs {
		if len(l.Message) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(l.Message[0], &s) != nil {
			continue
		}
		var v struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal([]byte(s), &v) == nil && v.Msg == "visit" {
			lines = append(lines, []byte(s))
		}
	}
	return lines, notice
}

func (t *tailer) once(ray string) bool {
	if ray == "" {
		return true
	}
	if _, dup := t.seen[ray]; dup {
		return false
	}
	now := time.Now()
	t.seen[ray] = now
	if now.Sub(t.swept) > time.Minute {
		t.swept = now
		for r, at := range t.seen {
			if now.Sub(at) > 5*time.Minute {
				delete(t.seen, r)
			}
		}
	}
	return true
}

func (t *tailer) write(line []byte) {
	var v struct {
		CF map[string]string `json:"cf"`
	}
	_ = json.Unmarshal(line, &v)
	if !t.once(v.CF["cf-ray"]) {
		return
	}
	t.out.Write(append(line, '\n'))
}

func renewAt(s tailSession, now time.Time) time.Time {
	life := s.expires.Sub(now)
	if life <= 0 {
		return now
	}
	return now.Add(life * 4 / 5)
}

func (t *tailer) run(ctx context.Context) error {
	type live struct {
		session tailSession
		conn    *websocket.Conn
		done    chan error
	}
	var cur *live
	start := func() error {
		s, conn, err := t.open(ctx)
		if err != nil {
			return err
		}
		l := &live{s, conn, make(chan error, 1)}
		go t.read(ctx, conn, l.done)
		go t.ping(ctx, conn, l.done, pingEvery, pongWait)
		if cur != nil {
			old := cur
			t.closing.Go(func() { t.close(old.session, old.conn) })
		}
		cur = l
		return nil
	}
	backoff := time.Second
	retry := func(err error) error {
		var api apiError
		if errors.As(err, &api) && (api.status == http.StatusUnauthorized || api.status == http.StatusForbidden) {
			return err
		}
		fmt.Fprintln(os.Stderr, "citrine-visits:", t.worker+":", err, "— trying again in", backoff)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Minute)
		return nil
	}
	for cur == nil {
		if err := start(); err != nil {
			if err := retry(err); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
		}
	}
	backoff = time.Second
	renew := time.NewTimer(time.Until(renewAt(cur.session, time.Now())))
	defer func() {
		if cur != nil {
			t.close(cur.session, cur.conn)
		}
		t.closing.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-t.lines:
			lines, notice := visitLines(msg)
			if notice != "" {
				fmt.Fprintln(os.Stderr, "citrine-visits:", t.worker+":", notice)
			}
			for _, l := range lines {
				t.write(l)
			}
		case <-renew.C:
			if err := start(); err != nil {
				if err := retry(err); err != nil {
					return err
				}
				renew.Reset(0)
				continue
			}
			backoff = time.Second
			renew.Reset(time.Until(renewAt(cur.session, time.Now())))
		case err := <-cur.done:
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintln(os.Stderr, "citrine-visits:", t.worker, "tail closed:", err)
			closed := cur
			t.closing.Go(func() { t.close(closed.session, closed.conn) })
			cur = nil
			for cur == nil && ctx.Err() == nil {
				if err := start(); err != nil {
					if err := retry(err); err != nil {
						return err
					}
				}
			}
			if cur == nil {
				return nil
			}
			backoff = time.Second
			renew.Reset(time.Until(renewAt(cur.session, time.Now())))
		}
	}
}

func runVisits(cfg config) int {
	token, zone := cfg["CF_API_TOKEN"], cfg["CF_ZONE_ID"]
	if cfg["SHIELD"] != "yes" {
		fmt.Fprintln(os.Stderr, "citrine-visits: the shield is off, so nothing to tail")
		return 0
	}
	if token == "" || zone == "" {
		fmt.Fprintln(os.Stderr, "citrine-visits: no CF_API_TOKEN or CF_ZONE_ID, so nothing to tail")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	var z struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := cfCall(ctx, token, http.MethodGet, "/zones/"+zone, nil, &z); err != nil {
		fmt.Fprintln(os.Stderr, "citrine-visits: can't read the zone:", err)
		return 1
	}
	tailers := []*tailer{{worker: "citrine-shield"}}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failed := make(chan error, len(tailers))
	var wg sync.WaitGroup
	for _, t := range tailers {
		t.token, t.account, t.out = token, z.Account.ID, os.Stdout
		t.seen, t.lines = map[string]time.Time{}, make(chan []byte, 64)
		wg.Go(func() {
			if err := t.run(ctx); err != nil {
				failed <- fmt.Errorf("%s: %w", t.worker, err)
				cancel()
			}
		})
	}
	wg.Wait()
	select {
	case err := <-failed:
		fmt.Fprintln(os.Stderr, "citrine-visits:", err)
		return 1
	default:
		return 0
	}
}
