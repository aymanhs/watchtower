package demo

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/objstore"
	"aymanhs/watchtower/internal/scheduler"
)

// ObjectTasks gives every fake server a "processes" and a "containers"
// object, fed through the same pipeline as live object collection.
func ObjectTasks(events *event.Engine, objects *objstore.Store) []scheduler.Task {
	var tasks []scheduler.Task
	for _, srv := range Fleet() {
		for _, o := range []struct {
			name     string
			interval time.Duration
			gen      func(*rand.Rand, time.Time) any
		}{
			{"processes", 15 * time.Second, processes},
			{"containers", 30 * time.Second, containers},
		} {
			rng := rand.New(rand.NewSource(int64(len(srv.Name)) * 7919))
			gen := o.gen
			def := &metric.Definition{Name: o.name, Kind: metric.KindObject}
			tasks = append(tasks, &app.ObjectTask{
				ServerName: srv.Name, Def: def, Events: events, Objects: objects,
				CollectInterval: o.interval, Deadline: o.interval / 2,
				Collect: func(context.Context) (json.RawMessage, error) {
					return json.Marshal(gen(rng, time.Now()))
				},
			})
		}
	}
	return tasks
}

func processes(rng *rand.Rand, _ time.Time) any {
	type proc struct {
		PID  int     `json:"pid"`
		User string  `json:"user"`
		CPU  float64 `json:"cpu"`
		Mem  float64 `json:"mem"`
		Cmd  string  `json:"cmd"`
	}
	cmds := []string{"nats-server", "postgres", "node", "python3", "java", "nginx", "dockerd", "redis-server"}
	out := make([]proc, 0, 6)
	cpu := 40 + rng.Float64()*30
	for i := 0; i < 6; i++ {
		out = append(out, proc{PID: 1000 + rng.Intn(9000), User: []string{"root", "app", "postgres"}[rng.Intn(3)],
			CPU: float64(int(cpu*10)) / 10, Mem: float64(int(rng.Float64()*120)) / 10, Cmd: cmds[rng.Intn(len(cmds))]})
		cpu *= 0.5
	}
	return out
}

// containers flips one container's state every few minutes so the log
// shows real changes between otherwise identical samples.
func containers(_ *rand.Rand, now time.Time) any {
	type ctr struct {
		Name   string `json:"name"`
		Image  string `json:"image"`
		State  string `json:"state"`
		Status string `json:"status"`
	}
	state, status := "running", "Up 3 days"
	if now.Minute()/5%2 == 1 {
		state, status = "restarting", "Restarting (1) 4 seconds ago"
	}
	return []ctr{
		{"web", "nginx:1.27", "running", "Up 3 days"},
		{"api", "myorg/api:2.4.1", state, status},
		{"db", "postgres:16", "running", "Up 12 days"},
		{"cache", "redis:7", "running", "Up 12 days"},
	}
}
