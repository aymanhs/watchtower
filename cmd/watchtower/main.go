// Command watchtower is the Phase 1/2 proof-of-concept: load metric
// definitions and server configs, connect over SSH, collect the resolved
// metrics on independent per-metric/per-server schedules under a bounded
// concurrency cap, and print observations to the console. No storage, no
// events, no GUI yet.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/group"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/server"
	"aymanhs/watchtower/internal/sshmgr"
)

// maxConcurrentCollections bounds how many SSH commands may be in flight at
// once across every server and metric combined. TODO: make configurable.
const maxConcurrentCollections = 16

// connectWarmupTimeout bounds the up-front connection attempt per server.
// It is independent of any metric's own collection timeout, so a slow
// first handshake never eats into a metric's execution budget.
const connectWarmupTimeout = 10 * time.Second

// loadGroupRegistry loads group-level metric overrides from dir. A missing
// groups directory is not an error — groups are optional; a server can
// list a group that carries no file and simply gets no group-level
// overrides for it.
func loadGroupRegistry(dir string) (*group.Registry, error) {
	configs, err := group.LoadGroups(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Printf("no %s directory found, no group-level overrides", dir)
			return group.NewRegistry(nil)
		}
		return nil, err
	}
	return group.NewRegistry(configs)
}

// connectAll dials every server concurrently before scheduling starts. A
// server that's slow or unreachable is logged and left to the normal
// lazy-reconnect path in sshmgr.Client.Run — it never blocks or fails the
// others.
func connectAll(ctx context.Context, clients map[string]*sshmgr.Client) {
	var wg sync.WaitGroup
	for name, client := range clients {
		name, client := name, client
		wg.Add(1)
		go func() {
			defer wg.Done()
			connectCtx, cancel := context.WithTimeout(ctx, connectWarmupTimeout)
			defer cancel()
			if err := client.Connect(connectCtx); err != nil {
				log.Printf("server %s: initial connect failed, will retry lazily: %v", name, err)
				return
			}
			log.Printf("server %s: connected", name)
		}()
	}
	wg.Wait()
}

func main() {
	defs, err := metric.LoadDefinitions("metrics")
	if err != nil {
		log.Fatalf("load metric definitions: %v", err)
	}
	registry, err := metric.NewRegistry(defs)
	if err != nil {
		log.Fatalf("build metric registry: %v", err)
	}
	log.Printf("loaded %d metric definitions", len(defs))

	servers, err := server.LoadServers("data/servers")
	if err != nil {
		log.Fatalf("load server configs: %v", err)
	}
	if len(servers) == 0 {
		log.Fatal("no servers configured in data/servers (gitignored — see README)")
	}
	log.Printf("loaded %d server configs", len(servers))

	groupReg, err := loadGroupRegistry("groups")
	if err != nil {
		log.Fatalf("load group configs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
	}()

	mgr := sshmgr.NewManager()
	events := event.NewEngine(0)

	clients := make(map[string]*sshmgr.Client, len(servers))
	for _, cfg := range servers {
		clients[cfg.Name] = mgr.Client(cfg)
	}
	connectAll(ctx, clients)

	var tasks []scheduler.Task
	for _, cfg := range servers {
		resolved := registry.ResolveGroups(cfg.Groups)
		if len(resolved) == 0 {
			log.Printf("server %s: no metrics resolved from groups %v", cfg.Name, cfg.Groups)
			continue
		}
		client := clients[cfg.Name]
		for _, def := range resolved {
			interval, timeout := effective(groupReg, cfg, def)
			log.Printf("server %s: scheduling %s every %s (timeout %s)", cfg.Name, def.Name, interval, timeout)
			tasks = append(tasks, &metricTask{
				serverName: cfg.Name,
				def:        def,
				client:     client,
				interval:   interval,
				timeout:    timeout,
				events:     events,
			})
		}
	}

	sched := scheduler.New(maxConcurrentCollections, func(taskName string, err error) {
		log.Printf("[%s] ERROR: %v", taskName, err)
	})
	sched.Run(ctx, tasks)
	log.Println("all collectors stopped")
}

// effective resolves def's interval and timeout for cfg, applying the full
// precedence chain: definition defaults, then cfg's groups' overrides in
// the order cfg lists them, then cfg's own server-specific override last.
func effective(groupReg *group.Registry, cfg *server.Config, def *metric.Definition) (interval, timeout time.Duration) {
	overrides := groupReg.Overrides(cfg.Groups, def.Name)
	if serverOverride, ok := cfg.Metrics[def.Name]; ok {
		overrides = append(overrides, serverOverride)
	}
	interval, timeout, _ = def.Resolve(overrides...)
	return interval, timeout
}

// metricTask adapts one (server, metric definition) pair to scheduler.Task.
// It is the only place that bridges the generic scheduler to the
// SSH/collect machinery and the event engine.
type metricTask struct {
	serverName        string
	def               *metric.Definition
	client            *sshmgr.Client
	interval, timeout time.Duration
	events            *event.Engine
}

func (t *metricTask) Name() string            { return t.serverName + "/" + t.def.Name }
func (t *metricTask) Interval() time.Duration { return t.interval }
func (t *metricTask) Timeout() time.Duration  { return t.timeout }

func (t *metricTask) Run(ctx context.Context) error {
	obs, err := collect.Execute(ctx, t.client, t.serverName, t.def, t.timeout)
	if opEv := t.events.EvaluateCollectorResult(t.serverName, t.def.Name, err, time.Now()); opEv != nil {
		printEvent(*opEv)
	}
	if err != nil {
		return err
	}
	for _, o := range obs {
		fmt.Printf("%-32s = %-16v %s\n", o.FullName(), o.Value, o.Timestamp.Format(time.RFC3339))
		measurement := strings.TrimPrefix(o.Metric, t.def.Name+".")
		if ev := t.events.EvaluateThreshold(t.serverName, t.def, measurement, o.Value, o.Timestamp); ev != nil {
			printEvent(*ev)
		}
	}
	return nil
}

func printEvent(ev event.Event) {
	switch ev.Kind {
	case event.KindRaised:
		fmt.Printf("!!! EVENT  %-8s %-10s %s/%-24s value=%v threshold=%v %q\n",
			ev.Severity, ev.Kind, ev.Server, ev.Metric, ev.Value, ev.Threshold, ev.Message)
	case event.KindRecovered:
		sev := ev.Severity
		if sev == "" {
			sev = "normal"
		}
		fmt.Printf("!!! EVENT  %-8s %-10s %s/%-24s value=%v %q\n",
			sev, ev.Kind, ev.Server, ev.Metric, ev.Value, ev.Message)
	}
}
