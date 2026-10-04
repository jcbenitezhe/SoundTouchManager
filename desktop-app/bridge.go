//go:build android || stmbridge

// Bridge entry point: the same App, served to a plain web view over loopback
// HTTP instead of a Wails window. The Android app starts this binary, points
// its WebView at it, and the frontend's bridge.js stands in for the Wails IPC.
//
// Contract with the host process:
//
//	STM_BRIDGE_ADDR    listen address, default 127.0.0.1:0 (any free port);
//	                   falls back to a free port when it is taken
//	STM_BRIDGE_TOKEN   shared secret for /api/*; generated and printed when unset
//	STM_BRIDGE_STDIN=1 exit when stdin closes, so the backend dies with its host
//
// Once listening, one line "STM_BRIDGE_LISTEN <host:port>" goes to stdout.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

type eventBroker struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

var bridgeEvents = &eventBroker{subs: map[chan []byte]struct{}{}}

type bridgeEvent struct {
	Name string `json:"name"`
	Data []any  `json:"data"`
}

// publish fans an event out to every open event stream. A subscriber that is
// not keeping up loses the event rather than stalling the emitter, which is
// often an install or update goroutine.
func (b *eventBroker) publish(name string, data []any) {
	if data == nil {
		data = []any{}
	}
	msg, err := json.Marshal(bridgeEvent{Name: name, Data: data})
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *eventBroker) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

type bridgeServer struct {
	app    *App
	token  string
	addr   string
	static http.Handler
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

func main() {
	defer func() {
		if r := recover(); r != nil {
			logCrash("bridge", r)
			panic(r)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("STM_BRIDGE_STDIN") == "1" {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			stop()
		}()
	}

	srv, serve, printURL, err := startBridge(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bridge:", err)
		os.Exit(1)
	}
	out := bufio.NewWriter(os.Stdout)
	fmt.Fprintln(out, "STM_BRIDGE_LISTEN", srv.addr)
	if printURL {
		fmt.Fprintf(out, "STM_BRIDGE_URL http://%s/?stmToken=%s\n", srv.addr, srv.token)
	}
	_ = out.Flush()

	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "bridge: serve:", err)
		os.Exit(1)
	}
}

// startBridge starts the App and binds the listener. serve blocks until ctx is
// done; printURL reports that the token was generated here rather than handed
// in by the host.
func startBridge(ctx context.Context) (srv *bridgeServer, serve func() error, printURL bool, err error) {
	net.DefaultResolver.PreferGo = true
	bridgePlatformInit()

	addr := os.Getenv("STM_BRIDGE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && os.Getenv("STM_BRIDGE_ADDR") != "" {
		// The host asks for a fixed port so the page origin, and with it the
		// web view's localStorage, stays the same across launches. A port some
		// other app holds must not keep the backend from starting at all.
		fmt.Fprintln(os.Stderr, "bridge: fixed address unavailable, using a free port:", err)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("listen: %w", err)
	}

	token := os.Getenv("STM_BRIDGE_TOKEN")
	printURL = token == ""
	if printURL {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			_ = ln.Close()
			return nil, nil, false, fmt.Errorf("token: %w", err)
		}
		token = hex.EncodeToString(buf)
	}

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		_ = ln.Close()
		return nil, nil, false, fmt.Errorf("assets: %w", err)
	}
	app := NewApp()
	srv = &bridgeServer{
		app:    app,
		token:  token,
		addr:   ln.Addr().String(),
		static: http.FileServer(http.FS(dist)),
	}
	app.startup(ctx)

	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	serve = func() error {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutdownCtx)
		}()
		for {
			err := httpSrv.Serve(ln)
			if err == nil || errors.Is(err, http.ErrServerClosed) || ctx.Err() != nil {
				return nil
			}
			if !bridgeRelisten {
				return err
			}
			slog.Warn("bridge: listener lost, taking the address again", slog.Any("err", err))
			for {
				time.Sleep(time.Second)
				if ctx.Err() != nil {
					return nil
				}
				if ln, err = net.Listen("tcp", srv.addr); err == nil {
					break
				}
			}
		}
	}
	return srv, serve, printURL, nil
}

func (s *bridgeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A page on some other origin can reach 127.0.0.1 through DNS rebinding;
	// its requests then carry the attacker's host name, never ours.
	if !s.hostAllowed(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/call/"):
		if !s.authorized(r.Header.Get("X-STM-Token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.serveCall(w, r, strings.TrimPrefix(r.URL.Path, "/api/call/"))
	case r.URL.Path == "/api/events":
		if !s.authorized(r.URL.Query().Get("t")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.serveEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		http.NotFound(w, r)
	default:
		w.Header().Set("Cache-Control", "no-store")
		s.static.ServeHTTP(w, r)
	}
}

func (s *bridgeServer) hostAllowed(host string) bool {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return false
	}
	return host == net.JoinHostPort("127.0.0.1", port) || host == net.JoinHostPort("localhost", port)
}

func (s *bridgeServer) authorized(got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

type callResponse struct {
	Result any    `json:"result"`
	Error  string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// serveCall invokes one exported App method the way the Wails binding layer
// does: positional JSON arguments in, the first result out, and a trailing
// non-nil error turned into a rejection carrying its message.
func (s *bridgeServer) serveCall(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := reflect.ValueOf(s.app).MethodByName(name)
	if name == "" || !m.IsValid() {
		writeJSON(w, callResponse{Error: "STM_MISSING_BINDING: " + name + " is not available in this build"})
		return
	}
	var raw []json.RawMessage
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err == nil && len(body) > 0 {
		err = json.Unmarshal(body, &raw)
	}
	if err != nil {
		writeJSON(w, callResponse{Error: "bad arguments: " + err.Error()})
		return
	}
	mt := m.Type()
	if mt.IsVariadic() {
		writeJSON(w, callResponse{Error: name + " cannot be called over the bridge"})
		return
	}
	in := make([]reflect.Value, mt.NumIn())
	for i := range in {
		p := reflect.New(mt.In(i))
		if i < len(raw) && len(raw[i]) > 0 && string(raw[i]) != "null" {
			if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
				writeJSON(w, callResponse{Error: fmt.Sprintf("argument %d of %s: %v", i+1, name, err)})
				return
			}
		}
		in[i] = p.Elem()
	}
	resp := s.invoke(name, m, in)
	writeJSON(w, resp)
}

func (s *bridgeServer) invoke(name string, m reflect.Value, in []reflect.Value) (resp callResponse) {
	defer func() {
		if r := recover(); r != nil {
			s.app.logger.Error("bridge call panicked", "method", name, "panic", r)
			resp = callResponse{Error: fmt.Sprintf("%s failed: %v", name, r)}
		}
	}()
	outs := m.Call(in)
	if n := len(outs); n > 0 && m.Type().Out(n-1) == errorType {
		if e := outs[n-1]; !e.IsNil() {
			return callResponse{Error: e.Interface().(error).Error()}
		}
		outs = outs[:n-1]
	}
	if len(outs) > 0 {
		resp.Result = outs[0].Interface()
	}
	return resp
}

func (s *bridgeServer) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsubscribe := bridgeEvents.subscribe()
	defer unsubscribe()
	fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
