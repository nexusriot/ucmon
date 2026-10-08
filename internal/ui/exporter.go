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
	cfg config.Config

	mu     sync.Mutex
	model  *Model
	lastAt time.Time
	err    error
}

// NewExporter builds an exporter for cfg.
func NewExporter(cfg config.Config) *Exporter {
	cfg.Sanitize()
	return &Exporter{cfg: cfg}
}

// snapshot returns a model holding readings no older than exporterMinInterval,
// collecting a fresh set when the cached one has expired.
func (e *Exporter) snapshot() (*Model, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.model != nil && time.Since(e.lastAt) < exporterMinInterval {
		return e.model, e.err
	}

	m, err := collectOnce(e.cfg)
	e.lastAt = time.Now()
	if err != nil {
		e.err = err
		return nil, err
	}
	e.model, e.err = &m, nil
	return e.model, nil
}

// Handler is the exporter's HTTP routing: /metrics for Prometheus, /snapshot
// for JSON, and a one-line index so a browser lands somewhere useful.
func (e *Exporter) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		m, err := e.snapshot()
		if err != nil {
			http.Error(w, "ucmon: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, m.buildPrometheus())
	})

	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		m, err := e.snapshot()
		if err != nil {
			http.Error(w, "ucmon: "+err.Error(), http.StatusInternalServerError)
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
