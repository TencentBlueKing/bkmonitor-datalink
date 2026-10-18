// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package taskdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/taskdispatch/observation"
	"testing/synctest"
)

type coordinatorRedisHook struct{ leaseLost bool }

func (h coordinatorRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("test must not dial Redis")
	}
}

func (h coordinatorRedisHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, command redis.Cmder) error {
		switch cmd := command.(type) {
		case *redis.Cmd:
			if h.leaseLost {
				cmd.SetVal(int64(0))
			} else {
				cmd.SetVal(int64(1))
			}
		case *redis.StringCmd:
			raw, _ := json.Marshal(newState())
			cmd.SetVal(string(raw))
		default:
			return fmt.Errorf("unexpected Redis command: %s", cmd.Name())
		}
		return nil
	}
}

func (h coordinatorRedisHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error { return errors.New("unexpected pipeline") }
}

type faultyCoordinatorSources struct {
	emptySources
	calls      int
	panicFirst bool
}

func (s *faultyCoordinatorSources) List(context.Context, string, string, int) ([]json.RawMessage, error) {
	s.calls++
	if s.calls == 1 {
		if s.panicFirst {
			panic("private source payload")
		}
		return nil, errors.New("source storage unavailable")
	}
	return nil, nil
}

type coordinatorFailureObserver struct {
	noopObserver
	cancel    context.CancelFunc
	failures  int
	succeeded int
}

func (o *coordinatorFailureObserver) ReconcileFailed(context.Context, error) { o.failures++ }

func (o *coordinatorFailureObserver) ControllerSnapshot(context.Context, observation.ControllerObservation) {
	o.succeeded++
	o.cancel()
}

func TestCoordinatorKeepsLeaseAndRecoversFromSourceFailureAndPanic(t *testing.T) {
	for _, panicFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			client.AddHook(coordinatorRedisHook{})
			defer func() { _ = client.Close() }()
			docs := &faultyCoordinatorSources{panicFirst: panicFirst}
			observer := &coordinatorFailureObserver{cancel: cancel}
			controller := &Controller{client: client, sources: eventsource.New(docs, config.SeverityConfig{}), observer: observer, key: "state", leader: "leader", token: "test"}
			if err := controller.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if docs.calls != 2 || observer.failures != 1 || observer.succeeded != 1 {
				t.Fatalf("calls=%d failures=%d successes=%d", docs.calls, observer.failures, observer.succeeded)
			}
		})
	}
}

func TestCoordinatorStillStopsWhenLeaseIsLost(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	client.AddHook(coordinatorRedisHook{leaseLost: true})
	defer func() { _ = client.Close() }()
	controller := &Controller{client: client, observer: noopObserver{}, key: "state", leader: "leader", token: "old-owner"}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := controller.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("lost authority was ignored", err)
	}
}
