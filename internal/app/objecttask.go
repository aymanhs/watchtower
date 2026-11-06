package app

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"aymanhs/watchtower/internal/collect"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/objstore"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// ObjectTask collects one object definition for one server on its own
// (usually slower) interval and stores the value. If the definition has
// emit expressions, the derived numbers go through the normal metric
// pipeline (events, state, history) under "<name>.<key>". A failure,
// including an oversized object, is a collector failure like any
// metric's; a persistence failure is only logged.
type ObjectTask struct {
	ServerName      string
	Def             *metric.Definition
	Client          collect.Runner
	CollectInterval time.Duration
	Deadline        time.Duration
	Config          map[string]any
	Events          *event.Engine
	Objects         *objstore.Store
	Store           *state.Store   // optional; only used for emitted metrics
	History         *storage.Store // optional; only used for emitted metrics
	// Collect, if non-nil, replaces the real collector (demo fixtures).
	Collect func(ctx context.Context) (json.RawMessage, error)
}

func (t *ObjectTask) Name() string            { return t.ServerName + "/" + t.Def.Name }
func (t *ObjectTask) Interval() time.Duration { return t.CollectInterval }
func (t *ObjectTask) Timeout() time.Duration  { return t.Deadline }

func (t *ObjectTask) Run(ctx context.Context) error {
	if len(t.Def.Emit) > 0 {
		return (&MetricTask{
			ServerName: t.ServerName, Def: t.Def, CollectInterval: t.CollectInterval,
			Deadline: t.Deadline, Events: t.Events, Store: t.Store, History: t.History,
			Collect: t.collectEmitted,
		}).Run(ctx)
	}
	_, err := t.collect(ctx)
	t.Events.EvaluateCollectorResult(t.ServerName, t.Def.Name, err, time.Now())
	return err
}

// collect gets one value and stores it.
func (t *ObjectTask) collect(ctx context.Context) (json.RawMessage, error) {
	var (
		value json.RawMessage
		err   error
	)
	if t.Collect != nil {
		value, err = t.Collect(ctx)
	} else {
		value, err = collectObject(ctx, t)
	}
	if err != nil {
		return nil, err
	}
	if _, err := t.Objects.Put(t.Name(), time.Now(), value); err != nil {
		log.Printf("[%s] objects: %v", t.Name(), err)
	}
	return value, nil
}

func (t *ObjectTask) collectEmitted(ctx context.Context) ([]collect.Observation, error) {
	value, err := t.collect(ctx)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return nil, err
	}
	now := time.Now()
	var obs []collect.Observation
	for key, expr := range t.Def.Emit {
		e, err := metric.ParseEmit(expr)
		if err != nil {
			return nil, err
		}
		if n, ok := e.Eval(v); ok {
			obs = append(obs, collect.Observation{Server: t.ServerName, Metric: t.Def.Name + "." + key, Value: n, Timestamp: now})
		}
	}
	return obs, nil
}

func collectObject(ctx context.Context, t *ObjectTask) (json.RawMessage, error) {
	return collect.ExecuteObject(ctx, t.Client, t.Def, t.Deadline, t.Config)
}
