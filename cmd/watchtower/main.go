// Command watchtower is the Phase 1 proof-of-concept: load metric
// definitions and server configs, connect over SSH, collect the resolved
// metrics on independent per-metric/per-server schedules, and print
// observations to the console. No storage, no events, no GUI yet.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/server"
	"aymanhs/watchtower/internal/sshmgr"
)

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

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
	}()

	mgr := sshmgr.NewManager()

	var wg sync.WaitGroup
	for _, cfg := range servers {
		resolved := registry.ResolveGroups(cfg.Groups)
		if len(resolved) == 0 {
			log.Printf("server %s: no metrics resolved from groups %v", cfg.Name, cfg.Groups)
			continue
		}
		client := mgr.Client(cfg)
		for _, def := range resolved {
			interval, timeout := effective(cfg, def)
			log.Printf("server %s: scheduling %s every %s (timeout %s)", cfg.Name, def.Name, interval, timeout)
			wg.Add(1)
			go collectLoop(ctx, &wg, client, cfg.Name, def, interval, timeout)
		}
	}

	wg.Wait()
	log.Println("all collectors stopped")
}

// effective applies a server's per-metric override, if any, on top of the
// metric definition's defaults.
func effective(cfg *server.Config, def *metric.Definition) (interval, timeout time.Duration) {
	interval = def.Interval.Duration
	timeout = def.Timeout.Duration
	if override, ok := cfg.Metrics[def.Name]; ok {
		if override.Interval != nil {
			interval = override.Interval.Duration
		}
		if override.Timeout != nil {
			timeout = override.Timeout.Duration
		}
	}
	return interval, timeout
}

// collectLoop runs one metric, for one server, on its own ticker, in its
// own goroutine. A failure here never affects any other metric or server.
// Since each tick blocks on the previous run's result before the next can
// fire, and timeout < interval is enforced at definition-load time,
// executions never overlap.
func collectLoop(ctx context.Context, wg *sync.WaitGroup, client *sshmgr.Client, serverName string, def *metric.Definition, interval, timeout time.Duration) {
	defer wg.Done()

	run := func() {
		obs, err := collect.Execute(ctx, client, serverName, def, timeout)
		if err != nil {
			log.Printf("[%s/%s] ERROR: %v", serverName, def.Name, err)
			return
		}
		for _, o := range obs {
			fmt.Printf("%-32s = %-16v %s\n", o.FullName(), o.Value, o.Timestamp.Format(time.RFC3339))
		}
	}

	run() // collect immediately, don't wait a full interval for the first reading
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
