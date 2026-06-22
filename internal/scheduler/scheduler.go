package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/relay-monitor/relay/internal/check"
	"github.com/relay-monitor/relay/internal/db"
)

// OnResult is called after every probe with the monitor and its result.
type OnResult func(m *db.Monitor, r check.Result)

// Scheduler runs per-monitor goroutines on configurable intervals.
type Scheduler struct {
	db          *db.DB
	concurrency int
	onResult    OnResult
	workers     map[string]*worker
	mu          sync.Mutex
	sem         chan struct{}

	httpChecker      *check.HTTP
	tcpChecker       *check.TCP
	tlsChecker       *check.TLS
	heartbeatChecker *check.Heartbeat
}

type worker struct {
	cancel context.CancelFunc
}

func New(database *db.DB, concurrency int, onResult OnResult) *Scheduler {
	hb := &check.Heartbeat{
		GetLastPing: database.GetLastHeartbeat,
	}
	return &Scheduler{
		db:               database,
		concurrency:      concurrency,
		onResult:         onResult,
		workers:          make(map[string]*worker),
		sem:              make(chan struct{}, concurrency),
		httpChecker:      &check.HTTP{},
		tcpChecker:       &check.TCP{},
		tlsChecker:       &check.TLS{},
		heartbeatChecker: hb,
	}
}

// Add starts a worker goroutine for the given monitor.
func (s *Scheduler) Add(m *db.Monitor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if w, ok := s.workers[m.ID]; ok {
		w.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.workers[m.ID] = &worker{cancel: cancel}
	go s.run(ctx, m)
}

// Remove stops the worker for the given monitor ID.
func (s *Scheduler) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if w, ok := s.workers[id]; ok {
		w.cancel()
		delete(s.workers, id)
	}
}

// Update stops and restarts a worker with the new monitor config.
func (s *Scheduler) Update(m *db.Monitor) {
	s.Add(m)
}

// run is the per-monitor goroutine. It runs the checker every IntervalS seconds.
func (s *Scheduler) run(ctx context.Context, m *db.Monitor) {
	interval := time.Duration(m.IntervalS) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	// Run immediately on start.
	s.probe(ctx, m)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Reload monitor in case it was edited.
			fresh, err := s.db.GetMonitor(m.ID)
			if err != nil || fresh == nil {
				return
			}
			s.probe(ctx, fresh)
		}
	}
}

func (s *Scheduler) probe(ctx context.Context, m *db.Monitor) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	cfg := parseCfg(m.Config)

	var result check.Result
	switch m.Type {
	case "http", "https":
		result = s.httpChecker.Check(ctx, m.Target, cfg)
	case "tcp":
		result = s.tcpChecker.Check(ctx, m.Target, cfg)
	case "tls":
		result = s.tlsChecker.Check(ctx, m.Target, cfg)
	case "heartbeat":
		// For heartbeat, target is the monitor ID itself
		cfg["interval_s"] = m.IntervalS
		result = s.heartbeatChecker.Check(ctx, m.ID, cfg)
	default:
		log.Printf("scheduler: unknown monitor type %q for %s", m.Type, m.ID)
		return
	}

	c := &db.Check{
		MonitorID: m.ID,
		Region:    "local",
		Status:    result.Status,
		CheckedAt: result.CheckedAt,
	}
	if result.LatencyMs > 0 {
		c.LatencyMs.Valid = true
		c.LatencyMs.Int64 = result.LatencyMs
	}
	if result.Detail != "" {
		c.Detail.Valid = true
		c.Detail.String = result.Detail
	}

	if err := s.db.CreateCheck(c); err != nil {
		log.Printf("scheduler: save check for %s: %v", m.ID, err)
	}

	s.onResult(m, result)
}

func parseCfg(raw string) map[string]any {
	var m map[string]any
	if raw != "" && raw != "{}" {
		json.Unmarshal([]byte(raw), &m)
	}
	if m == nil {
		m = make(map[string]any)
	}
	return m
}
