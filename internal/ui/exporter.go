package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
)

// exporterMinInterval bounds how often a scrape may re-sample. Prometheus
// defaults to a 15s scrape; a careless 1s one would otherwise drive a full
// /proc walk on a CM4 as fast as it could ask.
const exporterMinInterval = 5 * time.Second

// Exporter serves the current readings over HTTP in the Prometheus text
// exposition format, plus the JSON snapshot. It samples lazily: nothing is
// collected until something scrapes, and a result is reused for
// exporterMinInterval so a tight scrape loop cannot peg a small board.
type Exporter struct {
	cfg     config.Config
	collect func(config.Config) Model // collectOnce; a var for tests
	wait    time.Duration             // how long a scrape waits on a collection

	mu      sync.Mutex
	model   *Model
	lastAt  time.Time
	running chan struct{} // non-nil while a collection is in flight
}

// exporterWait is how long a scrape waits for a collection before serving
// the previous one: under Prometheus's default 10s scrape timeout.
const exporterWait = 8 * time.Second

// errNoSnapshot is a scrape that came before any collection finished.
var errNoSnapshot = errors.New("no snapshot yet: the first collection is still running")

// NewExporter builds an exporter for cfg.
func NewExporter(cfg config.Config) *Exporter {
	cfg.Sanitize()
	return &Exporter{cfg: cfg, collect: collectOnce, wait: exporterWait}
}

// snapshot returns a model holding readings no older than exporterMinInterval,
// starting a fresh collection when the cached one has expired.
//
// The collection runs outside the lock and at most one at a time. A scrape
// waits for it until the request is cancelled or e.wait passes, then serves
// the previous snapshot: a probe stuck in the kernel (statfs on a dead NFS
// mount, a wedged vcgencmd) costs one goroutine, instead of holding the lock
// and hanging every scrape after it, forever.
func (e *Exporter) snapshot(ctx context.Context) (*Model, time.Time, error) {
	e.mu.Lock()
	if e.model != nil && time.Since(e.lastAt) < exporterMinInterval {
		m, at := e.model, e.lastAt
		e.mu.Unlock()
		return m, at, nil
	}
	if e.running == nil {
		done := make(chan struct{})
		e.running = done
		go func() {
			m := e.collect(e.cfg)
			e.mu.Lock()
			e.model, e.lastAt, e.running = &m, time.Now(), nil
			e.mu.Unlock()
			close(done)
		}()
	}
	done := e.running
	e.mu.Unlock()

	timer := time.NewTimer(e.wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	case <-timer.C:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.model == nil {
		return nil, time.Time{}, errNoSnapshot
	}
	return e.model, e.lastAt, nil
}

// Handler is the exporter's HTTP routing: /metrics for Prometheus, /snapshot
// for JSON, and a one-line index so a browser lands somewhere useful.
func (e *Exporter) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		m, at, err := e.snapshot(r.Context())
		if err != nil {
			http.Error(w, "ucmon: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, m.buildPrometheus())
		// A stale snapshot is served rather than none; say how stale.
		_, _ = fmt.Fprintf(w, "# HELP ucmon_snapshot_age_seconds Age of the readings served, in seconds.\n"+
			"# TYPE ucmon_snapshot_age_seconds gauge\nucmon_snapshot_age_seconds %.3f\n", time.Since(at).Seconds())
	})

	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		m, _, err := e.snapshot(r.Context())
		if err != nil {
			http.Error(w, "ucmon: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		body, err := m.buildJSONReport(time.Now())
		if err != nil {
			http.Error(w, "ucmon: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(w, body)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "ucmon %s\n\n  /metrics   Prometheus exposition\n  /snapshot  JSON snapshot\n  /healthz   liveness\n", version)
	})

	return mux
}

// Serve runs the exporter until ctx is cancelled. It binds before returning,
// so a port already in use is reported rather than discovered later from an
// empty graph.
func Serve(ctx context.Context, cfg config.Config, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           NewExporter(cfg).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		return <-done
	case err := <-done:
		return err
	}
}

// ListenAddr normalises a bare port ("9100") or a bare colon form (":9100")
// into something net.Listen accepts.
func ListenAddr(s string) string {
	if s == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return ":" + s
}
