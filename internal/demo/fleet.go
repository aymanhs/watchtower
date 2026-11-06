// Package demo provides synthetic fixtures — a fake fleet of servers whose
// CPU, memory, disk, network, docker and NATS measurements follow realistic
// curves — so dashboards and charts can be built and viewed without any real
// machine. Observations go through the normal MetricTask pipeline (events,
// live state, history); only the collector is replaced.
package demo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

const gib = 1 << 30

type disk struct {
	name     string // already sanitized mount name, e.g. "root"
	sizeGiB  float64
	basePct  float64
	growth   float64 // percentage points added over one sawtooth cycle
	cycleLen time.Duration
}

// Server is one fake machine's behavior profile.
type Server struct {
	Name    string
	Cluster string
	Metrics []string // metric definition names it reports

	cores    int
	memGiB   float64
	cpuBase  float64 // typical CPU %
	cpuSwing float64 // size of the slow wave
	spiky    bool    // periodic CPU bursts that cross alert thresholds
	memBase  float64
	leaky    bool // memory climbs, then resets, like a leak + restart
	disks    []disk
	netRate  float64 // bytes/s average
	netErrs  bool    // accumulates rx errors
	flaky    bool    // network collection periodically fails
	nats     bool
	docker   int // total containers
}

// Fleet returns the built-in fake servers.
func Fleet() []Server {
	return []Server{
		{Name: "web-1", Metrics: []string{"cpu", "memory", "disk", "network", "docker"}, cores: 8, memGiB: 32, cpuBase: 35, cpuSwing: 18, memBase: 55,
			disks: []disk{{"root", 200, 48, 6, 6 * time.Hour}}, netRate: 4e6, docker: 12},
		{Name: "web-2", Metrics: []string{"cpu", "memory", "disk", "network", "docker"}, cores: 8, memGiB: 32, cpuBase: 42, cpuSwing: 20, spiky: true, memBase: 62,
			disks: []disk{{"root", 200, 61, 8, 5 * time.Hour}}, netRate: 5e6, docker: 12},
		{Name: "db-1", Cluster: "db", Metrics: []string{"cpu", "memory", "disk", "network"}, cores: 16, memGiB: 128, cpuBase: 28, cpuSwing: 12, memBase: 78,
			disks: []disk{{"root", 100, 35, 2, 8 * time.Hour}, {"data", 2000, 79, 14, 4 * time.Hour}, {"var_log", 50, 55, 25, 3 * time.Hour}}, netRate: 9e6},
		{Name: "db-2", Cluster: "db", Metrics: []string{"cpu", "memory", "disk", "network"}, cores: 16, memGiB: 128, cpuBase: 22, cpuSwing: 10, memBase: 72,
			disks: []disk{{"root", 100, 33, 2, 8 * time.Hour}, {"data", 2000, 64, 10, 4 * time.Hour}}, netRate: 6e6, netErrs: true},
		{Name: "cache-1", Metrics: []string{"cpu", "memory", "network"}, cores: 4, memGiB: 16, cpuBase: 15, cpuSwing: 8, memBase: 40, leaky: true, netRate: 12e6},
		{Name: "nats-1", Cluster: "nats", Metrics: []string{"cpu", "memory", "network", "nats"}, cores: 4, memGiB: 8, cpuBase: 20, cpuSwing: 10, memBase: 45, netRate: 3e6, nats: true},
		{Name: "nats-2", Cluster: "nats", Metrics: []string{"cpu", "memory", "network", "nats"}, cores: 4, memGiB: 8, cpuBase: 24, cpuSwing: 10, spiky: true, memBase: 48, netRate: 3e6, nats: true},
		{Name: "nats-3", Cluster: "nats", Metrics: []string{"cpu", "memory", "network", "nats"}, cores: 4, memGiB: 8, cpuBase: 18, cpuSwing: 9, memBase: 44, netRate: 2.5e6, nats: true},
		{Name: "jumpbox", Metrics: []string{"cpu", "memory", "disk", "network"}, cores: 2, memGiB: 4, cpuBase: 5, cpuSwing: 3, memBase: 30,
			disks: []disk{{"root", 40, 52, 3, 7 * time.Hour}}, netRate: 2e5, flaky: true},
	}
}

var errUnreachable = errors.New("demo: simulated collection failure")

// Sample returns the measurements metricName reports for s at t, keyed as
// a collector would emit them (instance-qualified where applicable).
func (s Server) Sample(metricName string, t time.Time) (map[string]float64, error) {
	// The flaky server also drops off entirely now and then, long enough
	// to leave a visible gap in 1d and 7d charts.
	if s.flaky && burst(s.Name+"down", t, 29*time.Hour, 4*time.Hour) > 0 {
		return nil, errUnreachable
	}
	switch metricName {
	case "cpu":
		return s.cpu(t), nil
	case "memory":
		return s.memory(t), nil
	case "disk":
		return s.disk(t), nil
	case "network":
		if s.flaky && burst(s.Name+"fail", t, 25*time.Minute, 3*time.Minute) > 0 {
			return nil, errUnreachable
		}
		return s.network(t), nil
	case "docker":
		return s.dockerStats(t), nil
	case "nats":
		return s.natsStats(t), nil
	}
	return nil, fmt.Errorf("demo: no generator for metric %q", metricName)
}

func (s Server) cpuUsage(t time.Time) float64 {
	seed := s.Name + "cpu"
	v := s.cpuBase +
		s.cpuSwing*wave(seed, t, 47*time.Minute) +
		s.cpuSwing*0.4*wave(seed, t, 11*time.Minute) +
		s.cpuSwing*0.5*smoothNoise(seed, t, 45*time.Second) +
		2*noise(seed, t)
	if s.spiky {
		v += 55 * burst(seed, t, 38*time.Minute, 6*time.Minute)
	}
	return clamp(v, 0.5, 100)
}

func (s Server) cpu(t time.Time) map[string]float64 {
	cores := float64(s.cores)
	avg := func(window time.Duration) float64 {
		const n = 6
		var sum float64
		for i := 0; i < n; i++ {
			sum += s.cpuUsage(t.Add(-window * time.Duration(i) / n))
		}
		return sum / n
	}
	return map[string]float64{
		"usage":  s.cpuUsage(t),
		"load1":  cores * avg(time.Minute) / 100,
		"load5":  cores * avg(5*time.Minute) / 100,
		"load15": cores * avg(15*time.Minute) / 100,
		"cores":  cores,
	}
}

func (s Server) memory(t time.Time) map[string]float64 {
	seed := s.Name + "mem"
	pct := s.memBase + 4*wave(seed, t, 3*time.Hour) + 1.5*smoothNoise(seed, t, time.Minute)
	if s.leaky {
		pct += 52 * sawtooth(seed, t, 2*time.Hour)
	}
	pct = clamp(pct, 5, 99)
	total := s.memGiB * gib
	used := total * pct / 100
	cache := (total - used) * (0.45 + 0.1*smoothNoise(seed+"cache", t, 5*time.Minute))
	return map[string]float64{
		"total":        total,
		"used":         used,
		"free":         total - used - cache,
		"shared":       total * 0.01,
		"cache":        cache,
		"available":    total - used,
		"used_percent": pct,
	}
}

func (s Server) disk(t time.Time) map[string]float64 {
	out := make(map[string]float64, len(s.disks)*4)
	for _, d := range s.disks {
		pct := clamp(d.basePct+d.growth*sawtooth(s.Name+d.name, t, d.cycleLen)+0.2*noise(s.Name+d.name, t), 1, 100)
		total := d.sizeGiB * gib
		out[d.name+".total"] = total
		out[d.name+".used"] = total * pct / 100
		out[d.name+".free"] = total * (100 - pct) / 100
		out[d.name+".used_percent"] = pct
	}
	return out
}

func (s Server) network(t time.Time) map[string]float64 {
	rx := counter(s.Name+"rx", t, s.netRate, 0.6, 53*time.Minute)
	tx := counter(s.Name+"tx", t, s.netRate*0.7, 0.5, 71*time.Minute)
	var errs float64
	if s.netErrs {
		errs = math.Floor(math.Max(0, secs(t)-bootEpoch) / 86400 / 4)
	}
	return map[string]float64{
		"rx_bytes":   math.Floor(rx),
		"rx_packets": math.Floor(rx / 900),
		"rx_errors":  errs,
		"tx_bytes":   math.Floor(tx),
		"tx_packets": math.Floor(tx / 900),
		"tx_errors":  0,
	}
}

func (s Server) dockerStats(t time.Time) map[string]float64 {
	bucket := t.Unix() / 900
	stopped := math.Floor(hash01(s.Name+"stopped", bucket) * 3)
	paused := 0.0
	if hash01(s.Name+"paused", bucket) > 0.85 {
		paused = 1
	}
	total := float64(s.docker)
	return map[string]float64{
		"containers_total":   total,
		"containers_running": total - stopped - paused,
		"containers_paused":  paused,
		"containers_stopped": stopped,
		"images":             total + 6,
	}
}

func (s Server) natsStats(t time.Time) map[string]float64 {
	seed := s.Name + "nats"
	conns := math.Floor(clamp(220+60*wave(seed, t, 41*time.Minute)+15*smoothNoise(seed, t, 30*time.Second), 0, 1e9))
	slow := 0.0
	if burst(seed+"slow", t, 55*time.Minute, 4*time.Minute) > 0.5 {
		slow = 1 + math.Floor(hash01(seed, t.Unix()/60)*3)
	}
	cpu := clamp(s.cpuUsage(t)*0.8, 0, 100)
	return map[string]float64{
		"connections":       conns,
		"total_connections": math.Floor(counter(seed+"c", t, 0.05, 0.3, time.Hour)),
		"subscriptions":     conns * 6,
		"slow_consumers":    slow,
		"cpu":               cpu,
		"mem":               (180 + 40*wave(seed+"m", t, 2*time.Hour)) * 1e6,
		"in_msgs":           math.Floor(counter(seed+"im", t, 4000, 0.5, 29*time.Minute)),
		"out_msgs":          math.Floor(counter(seed+"om", t, 12000, 0.5, 29*time.Minute)),
		"in_bytes":          math.Floor(counter(seed+"ib", t, 1.2e6, 0.5, 29*time.Minute)),
		"out_bytes":         math.Floor(counter(seed+"ob", t, 3.6e6, 0.5, 29*time.Minute)),
	}
}

func observations(server string, metricName string, values map[string]float64, t time.Time) []collect.Observation {
	obs := make([]collect.Observation, 0, len(values))
	for k, v := range values {
		obs = append(obs, collect.Observation{Server: server, Metric: metricName + "." + k, Value: v, Timestamp: t})
	}
	return obs
}

// Tasks builds one scheduler task per fake server/metric, using the real
// definitions (thresholds and intervals) from defs. Definitions a server
// lists but defs lacks are skipped.
func Tasks(defs []*metric.Definition, events *event.Engine, store *state.Store, history *storage.Store) []scheduler.Task {
	byName := make(map[string]*metric.Definition, len(defs))
	for _, d := range defs {
		byName[d.Name] = d
	}
	var tasks []scheduler.Task
	for _, srv := range Fleet() {
		if store != nil {
			store.SetCluster(srv.Name, srv.Cluster)
		}
		for _, name := range srv.Metrics {
			def, ok := byName[name]
			if !ok {
				continue
			}
			srv, name := srv, name
			tasks = append(tasks, &app.MetricTask{
				ServerName:      srv.Name,
				Def:             def,
				CollectInterval: def.Interval.Duration,
				Deadline:        def.Timeout.Duration,
				Events:          events,
				Store:           store,
				History:         history,
				Collect: func(context.Context) ([]collect.Observation, error) {
					now := time.Now()
					vals, err := srv.Sample(name, now)
					if err != nil {
						return nil, err
					}
					return observations(srv.Name, name, vals, now), nil
				},
			})
		}
	}
	return tasks
}

// Backfill writes span of past history ending at now, one point per step,
// so charts have something to show immediately. The target store should be
// empty: points are appended, not merged.
func Backfill(history *storage.Store, defs []*metric.Definition, now time.Time, span, step time.Duration) error {
	byName := make(map[string]*metric.Definition, len(defs))
	for _, d := range defs {
		byName[d.Name] = d
	}
	for t := now.Add(-span).Truncate(step); t.Before(now); t = t.Add(step) {
		for _, srv := range Fleet() {
			for _, name := range srv.Metrics {
				if _, ok := byName[name]; !ok {
					continue
				}
				vals, err := srv.Sample(name, t)
				if err != nil {
					continue
				}
				for _, o := range observations(srv.Name, name, vals, t) {
					if err := history.Append(o.FullName(), o.Timestamp, o.Value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
