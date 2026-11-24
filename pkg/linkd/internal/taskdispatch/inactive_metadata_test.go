// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package taskdispatch

import (
	"context"
	"encoding/json"
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type inactiveMetadataHook struct {
	coordinatorRedisHook
	state State
}

func (h *inactiveMetadataHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	fallback := h.coordinatorRedisHook.ProcessHook(next)
	return func(ctx context.Context, command redis.Cmder) error {
		if cmd, ok := command.(*redis.StringCmd); ok {
			b, _ := json.Marshal(h.state)
			cmd.SetVal(string(b))
			return nil
		}
		if cmd, ok := command.(*redis.Cmd); ok && len(cmd.Args()) > 1 && cmd.Args()[1] == commitScript {
			args := cmd.Args()
			var committed State
			if err := json.Unmarshal([]byte(args[len(args)-1].(string)), &committed); err != nil {
				return err
			}
			h.state = committed
			cmd.SetVal(int64(1))
			return nil
		}
		return fallback(ctx, command)
	}
}

func TestInactiveSourcesDoNotProbeOrRetainMetadataErrors(t *testing.T) {
	hook := &inactiveMetadataHook{state: newState()}
	docs := &releaseFailureDocuments{}
	for _, id := range []string{"disabled", "deleted"} {
		hook.state.Metadata[id] = Metadata{Error: "topic missing"}
		docs.records = append(docs.records, eventsource.Record{ID: id, Published: 1, Deleted: id == "deleted", Spec: config.EventSource{EventSourceID: id, Enabled: id == "deleted"}})
	}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	client.AddHook(hook)
	defer func() { _ = client.Close() }()
	observer := &releaseFailureObserver{}
	controller := &Controller{client: client, sources: eventsource.New(docs, config.SeverityConfig{}), observer: observer, key: "state", leader: "leader", token: "test"}
	if err := controller.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(hook.state.Metadata) != 0 {
		t.Fatalf("stale metadata retained: %+v", hook.state.Metadata)
	}
	for _, op := range observer.operations {
		if op == "kafka_probe" {
			t.Fatal("inactive source was probed")
		}
	}
}
