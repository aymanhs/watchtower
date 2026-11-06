package state

import (
	"sort"
	"sync"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// Store holds the live, continuously-updated state for every server,
// safe for concurrent writes from collector goroutines and concurrent
// reads from a GUI's render loop.
type Store struct {
	mu      sync.Mutex
	servers map[string]*serverEntry
}

type serverEntry struct {
	name    string
	cluster string
	metrics map[string]*MetricView
}

// NewStore returns an empty Store. Servers and metrics appear the first
// time they're updated — nothing needs pre-registering.
func NewStore() *Store {
	return &Store{servers: make(map[string]*serverEntry)}
}

func severityToStatus(s metric.Severity) Status {
	switch s {
	case metric.SeverityCritical:
		return StatusCritical
	case metric.SeverityWarning:
		return StatusWarning
	default:
		return StatusOK
	}
}

// UpdateSuccess records a successful collection: the given measurements
// replace the metric's last-known values, Stale clears, and Status
// reflects severity (the worst across the measurements' current event
// severities, as passed in measurementSeverity — keyed by measurement key,
// zero value meaning normal).
func (s *Store) UpdateSuccess(serverName, metricName string, measurements []MeasurementView, measurementSeverity map[string]metric.Severity, ts time.Time) {
	status := StatusOK
	for _, sev := range measurementSeverity {
		status = worst(status, severityToStatus(sev))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mv := s.entry(serverName, metricName)
	mv.Status = status
	mv.Stale = false
	mv.LastError = ""
	mv.Updated = ts
	mv.Measurements = append([]MeasurementView(nil), measurements...)
	sort.Slice(mv.Measurements, func(i, j int) bool { return mv.Measurements[i].Key < mv.Measurements[j].Key })
}

// SetCluster records serverName's cluster label — a GUI's grouping, not
// anything the engine interprets. Safe to call before any metric update
// (it creates the server entry if needed) or any time after; cluster is
// static per server for the process's lifetime, so there's no "clearing"
// operation, only setting it (including to "" for no cluster).
func (s *Store) SetCluster(serverName, cluster string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverEntry(serverName).cluster = cluster
}

// Retain drops every server whose name is not in names, so a server removed
// from the configuration disappears from the view.
func (s *Store) Retain(names []string) {
	keep := make(map[string]bool, len(names))
	for _, n := range names {
		keep[n] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for n := range s.servers {
		if !keep[n] {
			delete(s.servers, n)
		}
	}
}

// MarkStale records a failed collection: last-known measurements are kept
// exactly as they were, Stale is set, and Status becomes StatusUnknown —
// a failure must never look like the metric quietly returned to normal.
func (s *Store) MarkStale(serverName, metricName string, errMsg string, ts time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mv := s.entry(serverName, metricName)
	mv.Stale = true
	mv.LastError = errMsg
	mv.Updated = ts
	mv.Status = StatusUnknown
}

// serverEntry returns (creating if necessary) serverName's entry. Caller
// must hold s.mu.
func (s *Store) serverEntry(serverName string) *serverEntry {
	srv, ok := s.servers[serverName]
	if !ok {
		srv = &serverEntry{name: serverName, metrics: make(map[string]*MetricView)}
		s.servers[serverName] = srv
	}
	return srv
}

// entry returns (creating if necessary) the MetricView for serverName and
// metricName. Caller must hold s.mu.
func (s *Store) entry(serverName, metricName string) *MetricView {
	srv := s.serverEntry(serverName)
	mv, ok := srv.metrics[metricName]
	if !ok {
		mv = &MetricView{Name: metricName}
		srv.metrics[metricName] = mv
	}
	return mv
}

// Snapshot returns a deep copy of every server's current state, sorted by
// name, safe for a GUI to render without holding any lock.
func (s *Store) Snapshot() []ServerView {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]ServerView, 0, len(s.servers))
	for _, srv := range s.servers {
		sv := ServerView{Name: srv.name, Cluster: srv.cluster, Status: StatusOK}
		for _, mv := range srv.metrics {
			cp := *mv
			cp.Measurements = append([]MeasurementView(nil), mv.Measurements...)
			sv.Metrics = append(sv.Metrics, cp)
			sv.Status = worst(sv.Status, cp.Status)
		}
		sort.Slice(sv.Metrics, func(i, j int) bool { return sv.Metrics[i].Name < sv.Metrics[j].Name })
		out = append(out, sv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
