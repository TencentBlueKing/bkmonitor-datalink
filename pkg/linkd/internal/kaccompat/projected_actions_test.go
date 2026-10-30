// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/alertqueue"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type actionNotificationHook struct{ bodies []string }

func (h *actionNotificationHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *actionNotificationHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(context.Context, redis.Cmder) error { return errors.New("unexpected single notification") }
}

func (h *actionNotificationHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, commands []redis.Cmder) error {
		for i, c := range commands {
			h.bodies = append(h.bodies, fmt.Sprint(c.Args()[len(c.Args())-1]))
			c.(*redis.StringCmd).SetVal(fmt.Sprintf("%d-0", i+1))
		}
		return nil
	}
}

func TestProjectedCloseSchedulesOnlyActionWithoutReadingOrRewritingAlert(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:1"})
	t.Cleanup(func() { _ = client.Close() })
	hook := &actionNotificationHook{}
	client.AddHook(hook)
	var refreshes atomic.Int32
	writer := newClient(config.KACPluginConfig{}, closeTransportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/_refresh") {
			t.Error("projected close repeated KAC read or write", r.URL.Path)
		}
		refreshes.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}), nil, nil)
	writer.locations[alarmKey{"tenant", "closed"}] = "alarm-1"
	received := make(chan ActionRequest, 1)
	runtime := NewRuntime(config.KACDeliveryConfig{BatchWaitMilliseconds: 1}, client, "changes", readerFunc(func(context.Context, string, []string) (store.AlertBatch, error) {
		t.Error("projected close reread full Alert")
		return store.AlertBatch{}, errors.New("unexpected full read")
	}), writer, senderFunc(func(_ context.Context, q ActionRequest) error { received <- q; return nil }), nil, nil)
	action := &domain.AlertAction{ID: "stable-close-action", Type: "close", Cause: domain.AlertActionCause{Type: "user_operation", ID: "original-operation"}}
	changes := []domain.AlertChange{{BKTenantID: "tenant", AlertID: "closed", Action: action}, {BKTenantID: "tenant", AlertID: "no-action"}}
	for _, err := range runtime.PublishProjectedChanges(t.Context(), changes) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(hook.bodies) != 1 {
		t.Fatal("actionless close produced a duplicate notification", len(hook.bodies))
	}
	var notification alertqueue.Notification
	if err := json.Unmarshal([]byte(hook.bodies[0]), &notification); err != nil {
		t.Fatal(err)
	}
	if notification.ProjectionIndex != "alarm-1" || notification.Action.ID != action.ID {
		t.Fatal(notification)
	}
	if !runtime.dispatchNotification(t.Context(), "1-0", notification) {
		t.Fatal("action notification not accepted")
	}
	for _, lane := range runtime.lanes {
		if len(lane.items) != 0 {
			t.Fatal("projected close entered ordinary projection lane")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	workers.Go(func() { runtime.runRefresh(ctx) })
	workers.Go(func() { runtime.runAction(ctx) })
	defer func() { cancel(); workers.Wait() }()
	select {
	case q := <-received:
		if q.AlertID != "closed" || q.ID != action.ID || q.Cause.ID != "original-operation" {
			t.Fatal(q)
		}
	case <-time.After(time.Second):
		t.Fatal("close action was lost")
	}
	select {
	case id := <-runtime.completed:
		if id != "1-0" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("sent action was not settled")
	}
	if refreshes.Load() != 1 {
		t.Fatal("action refresh was lost or repeated", refreshes.Load())
	}
}

func TestCloseBulkDiagnosticsSeparateResponsesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		transportError error
		phase          string
	}{{"success", 200, nil, "succeeded"}, {"item-failure", 503, nil, "failed"}, {"canceled", 0, context.DeadlineExceeded, "canceled"}} {
		t.Run(tc.name, func(t *testing.T) {
			phases := map[string][]time.Duration{}
			ctx := store.WithBatchPhaseObserver(t.Context(), func(_ context.Context, p string, d time.Duration) { phases[p] = append(phases[p], d) })
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			requests := 0
			writer := newClient(config.KACPluginConfig{}, closeTransportFunc(func(*http.Request) (*http.Response, error) {
				requests++
				if tc.transportError != nil {
					cancel()
					return nil, ctx.Err()
				}
				body := fmt.Sprintf(`{"took":37,"items":[{"update":{"status":%d,"_index":"alarm-1"}}]}`, tc.status)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			}), nil, nil)
			writer.locations[alarmKey{"tenant", "closed"}] = "alarm-1"
			a := storetest.Alert("tenant", "closed", "event", "fingerprint", "warning")
			out := writer.WriteCloseBatch(ctx, []domain.Alert{a})
			phase := tc.phase
			if requests != 1 || len(phases["projection.bulk."+phase]) != 1 {
				t.Fatal(requests, phases)
			}
			if tc.transportError == nil {
				if len(phases["projection.es_bulk_took"]) != 1 || phases["projection.es_bulk_took"][0] != 37*time.Millisecond {
					t.Fatal(phases)
				}
			} else if len(phases["projection.es_bulk_took"]) != 0 {
				t.Fatal("invented ES took for a canceled request")
			}
			if (out[0].Err == nil) != (tc.status == 200 && tc.transportError == nil) {
				t.Fatal(out)
			}
		})
	}
}

func TestProjectedCloseKeepsPendingNotificationWhenActionBufferIsFull(t *testing.T) {
	runtime := NewRuntime(config.KACDeliveryConfig{ActionBuffer: 1}, nil, "", nil, nil, nil, nil, nil)
	runtime.actions <- actionJob{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	change := domain.AlertChange{BKTenantID: "tenant", AlertID: "closed", Action: &domain.AlertAction{ID: "close-action", Type: "close"}}
	if runtime.dispatchNotification(ctx, "pending-1", alertqueue.Notification{AlertChange: change, ProjectionIndex: "alarm-1"}) {
		t.Fatal("canceled close action was dropped or reported queued")
	}
	if len(runtime.completed) != 0 || len(runtime.actions) != 1 {
		t.Fatal("unstarted close action was acknowledged")
	}
}
