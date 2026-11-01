package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"sync"
	"time"

	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/group"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/server"
	"aymanhs/watchtower/internal/sshmgr"
	"aymanhs/watchtower/internal/state"
)

// Paths is where Bootstrap loads configuration from, relative to the
// working directory — the same fixed layout both binaries share.
type Paths struct {
	Metrics string // metric definition YAML, e.g. "metrics"
	Groups  string // group override YAML, e.g. "groups" (optional — missing dir is fine)
	Servers string // server config YAML, e.g. "data/servers"
}

// DefaultPaths is the layout used by both cmd/watchtower and
// cmd/watchtower-gui.
var DefaultPaths = Paths{Metrics: "metrics", Groups: "groups", Servers: "data/servers"}

// connectWarmupTimeout bounds the up-front connection attempt per server,
// independent of any metric's own collection timeout.
const connectWarmupTimeout = 10 * time.Second

// Bootstrap loads metric/group/server configuration, connects to every
// server up front, and returns one scheduler.Task per (server, metric)
// pair the servers' groups resolve to. events and store are wired into
// every task; store may be nil (no GUI state kept), print controls
// whether tasks also write to stdout.
func Bootstrap(ctx context.Context, paths Paths, events *event.Engine, store *state.Store, print bool) ([]scheduler.Task, error) {
	defs, err := metric.LoadDefinitions(paths.Metrics)
	if err != nil {
		return nil, fmt.Errorf("load metric definitions: %w", err)
	}
	registry, err := metric.NewRegistry(defs)
	if err != nil {
		return nil, fmt.Errorf("build metric registry: %w", err)
	}
	log.Printf("loaded %d metric definitions", len(defs))

	servers, err := server.LoadServers(paths.Servers)
	if err != nil {
		return nil, fmt.Errorf("load server configs: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers configured in %s (gitignored — see README)", paths.Servers)
	}
	log.Printf("loaded %d server configs", len(servers))

	groupReg, err := loadGroupRegistry(paths.Groups)
	if err != nil {
		return nil, fmt.Errorf("load group configs: %w", err)
	}

	mgr := sshmgr.NewManager()
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
		if store != nil {
			store.SetCluster(cfg.Name, cfg.Cluster)
		}
		for _, def := range resolved {
			interval, timeout, config := effective(groupReg, cfg, def)
			log.Printf("server %s: scheduling %s every %s (timeout %s)", cfg.Name, def.Name, interval, timeout)
			tasks = append(tasks, &MetricTask{
				ServerName:      cfg.Name,
				Def:             def,
				Client:          client,
				CollectInterval: interval,
				Deadline:        timeout,
				Config:          config,
				Events:          events,
				Store:           store,
				Print:           print,
			})
		}
	}
	return tasks, nil
}

// loadGroupRegistry loads group-level metric overrides from dir. A missing
// groups directory is not an error — groups are optional.
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

// effective resolves def's interval, timeout and collector config for cfg,
// applying the full precedence chain: definition defaults, then cfg's
// groups' overrides in the order cfg lists them, then cfg's own
// server-specific override last. config is what a parameterized
// collector's command template (e.g. a NATS server's "endpoint") renders
// against — empty for the common case of a definition with no parameters.
func effective(groupReg *group.Registry, cfg *server.Config, def *metric.Definition) (interval, timeout time.Duration, config map[string]any) {
	overrides := groupReg.Overrides(cfg.Groups, def.Name)
	if serverOverride, ok := cfg.Metrics[def.Name]; ok {
		overrides = append(overrides, serverOverride)
	}
	return def.Resolve(overrides...)
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
