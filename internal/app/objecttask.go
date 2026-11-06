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
)

// ObjectTask collects one object definition for one server on its own
// (usually slower) interval. A failure, including an oversized object, is
// a collector failure event like any metric's; a persistence failure is
// only logged.
type ObjectTask struct {
	ServerName      string
	Def             *metric.Definition
	Client          collect.Runner
	CollectInterval time.Duration
	Deadline        time.Duration
	Config          map[string]any
	Events          *event.Engine
	Objects         *objstore.Store
	// Collect, if non-nil, replaces the real collector (demo fixtures).
	Collect func(ctx context.Context) (json.RawMessage, error)
}

func (t *ObjectTask) Name() string            { return t.ServerName + "/" + t.Def.Name }
func (t *ObjectTask) Interval() time.Duration { return t.CollectInterval }
func (t *ObjectTask) Timeout() time.Duration  { return t.Deadline }

func (t *ObjectTask) Run(ctx context.Context) error {
	var (
		value json.RawMessage
		err   error
	)
	if t.Collect != nil {
		value, err = t.Collect(ctx)
	} else {
		value, err = collect.ExecuteObject(ctx, t.Client, t.Def, t.Deadline, t.Config)
	}
	now := time.Now()
	t.Events.EvaluateCollectorResult(t.ServerName, t.Def.Name, err, now)
	if err != nil {
		return err
	}
	if _, err := t.Objects.Put(t.Name(), now, value); err != nil {
		log.Printf("[%s] objects: %v", t.Name(), err)
	}
	return nil
}
