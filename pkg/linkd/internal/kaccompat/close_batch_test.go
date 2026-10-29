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
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type closeTransportFunc func(*http.Request) (*http.Response, error)

func (f closeTransportFunc) Perform(r *http.Request) (*http.Response, error) { return f(r) }

func TestCloseProjection5000PatchesOnlyFiveFields(t *testing.T) {
	requests, requestBytes := 0, 0
	client := newClient(config.KACPluginConfig{}, closeTransportFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Path != "/_bulk" || req.URL.Query().Get("refresh") != "true" {
			t.Fatal(req.URL)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		requestBytes += len(raw)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		items := []any{}
		for i := 0; i < len(lines); i += 2 {
			var head struct {
				Update map[string]any `json:"update"`
			}
			var body struct {
				Doc map[string]any `json:"doc"`
			}
			if err := json.Unmarshal([]byte(lines[i]), &head); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(lines[i+1]), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Doc) != 5 || body.Doc["status"] != "closed" || body.Doc["source_alarm_status"] != "close" {
				t.Fatal("not a terminal patch", body)
			}
			items = append(items, map[string]any{"update": map[string]any{"status": 200, "_index": head.Update["_index"]}})
		}
		body, err := json.Marshal(map[string]any{"items": items})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	}), nil, nil)
	alerts := make([]domain.Alert, 5000)
	content := strings.Repeat("x", 64<<10)
	for i := range alerts {
		a := storetest.Alert("tenant", fmt.Sprintf("alert-%04d", i), "opening", fmt.Sprint(i), "warning")
		at := a.UpdateAt
		a.EndAt, a.Status, a.EndType, a.EndReason = &at, domain.AlertStatusClosed, domain.AlertEndTypeUser, "done"
		a.Content = content
		alerts[i] = a
		client.locations[alarmKey{tenant: "tenant", alert: a.AlertID}] = "alarm-1"
	}
	for _, result := range client.WriteCloseBatch(t.Context(), alerts) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	if requests != 1 || requestBytes > 4<<20 {
		t.Fatal(requests, requestBytes)
	}
	t.Logf("5000 KAC close patches: requests=%d bytes=%d (local transport, not ES throughput)", requests, requestBytes)
}

type compactCloseReader struct {
	readerFunc
	states int
}

func (r *compactCloseReader) GetAlertCloseStates(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	r.states++
	return testReader(ctx, tenant, ids)
}

type compactCloseWriter struct {
	writerStub
	close func(context.Context, []domain.Alert) []WriteResult
}

func (w compactCloseWriter) WriteCloseBatch(ctx context.Context, alerts []domain.Alert) []WriteResult {
	return w.close(ctx, alerts)
}

func TestCloseProjectionReadsFullSnapshotOnlyForMissingKACDocument(t *testing.T) {
	reads, creates := 0, 0
	reader := &compactCloseReader{readerFunc: func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
		reads++
		if len(ids) != 1 || ids[0] != "missing" {
			t.Fatal("full snapshot read for existing KAC document", ids)
		}
		return testReader(ctx, tenant, ids)
	}}
	failure := errors.New("projection unavailable")
	writer := compactCloseWriter{writerStub: writerStub{write: func(_ context.Context, alerts []domain.Alert, refresh bool) []WriteResult {
		creates++
		if len(alerts) != 1 || alerts[0].AlertID != "missing" || !refresh {
			t.Fatal(alerts, refresh)
		}
		return []WriteResult{{}}
	}}, close: func(context.Context, []domain.Alert) []WriteResult {
		return []WriteResult{{}, {Err: store.ErrNotFound}, {Err: failure}}
	}}
	r := NewRuntime(config.KACDeliveryConfig{}, nil, "stream", reader, writer, nil, nil, nil)
	results := r.ProjectBatchNow(t.Context(), "tenant", []string{"existing", "missing", "failed"})
	if reader.states != 1 || reads != 1 || creates != 1 || results[0] != nil || results[1] != nil || !errors.Is(results[2], failure) {
		t.Fatal(results, reads, creates, reader.states)
	}
}

func TestCloseProjection500UsesOneReadAndOneBulkWithItemErrors(t *testing.T) {
	ids := make([]string, 500)
	for i := range ids {
		ids[i] = fmt.Sprintf("alert-%d", i)
	}
	reads, writes := 0, 0
	failed := errors.New("bulk item failed")
	r := NewRuntime(config.KACDeliveryConfig{}, nil, "stream", readerFunc(func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
		reads++
		if len(ids) != 500 {
			t.Fatal("read split per alert", len(ids))
		}
		// 缺失项不进入 Bulk，仍保留其输入位置。
		return testReader(ctx, tenant, ids[:499])
	}), writerStub{write: func(_ context.Context, alerts []domain.Alert, refresh bool) []WriteResult {
		writes++
		if !refresh || len(alerts) != 499 {
			t.Fatal("not a synchronous bulk", len(alerts), refresh)
		}
		results := make([]WriteResult, len(alerts))
		results[1].Err = failed
		return results
	}}, nil, nil, nil)
	results := r.ProjectBatchNow(t.Context(), "tenant", ids)
	if reads != 1 || writes != 1 || len(results) != 500 {
		t.Fatal(reads, writes, len(results))
	}
	for i, err := range results {
		switch i {
		case 1:
			if !errors.Is(err, failed) {
				t.Fatal(i, err)
			}
		case 499:
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatal(i, err)
			}
		default:
			if err != nil {
				t.Fatal(i, err)
			}
		}
	}
}

func TestCloseProjectionCancellationReleasesAcquiredLanes(t *testing.T) {
	r := NewRuntime(config.KACDeliveryConfig{ProjectionConcurrency: 2}, nil, "stream", readerFunc(func(context.Context, string, []string) (store.AlertBatch, error) {
		t.Error("read while lane unavailable")
		return store.AlertBatch{}, nil
	}), writerStub{}, nil, nil, nil)
	ids := []string{}
	for n := 0; len(ids) < 2; n++ {
		id := fmt.Sprintf("alert-%d", n)
		if r.lane("tenant", id) == &r.lanes[len(ids)] {
			ids = append(ids, id)
		}
	}
	r.lanes[1].gate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan []error, 1)
	go func() { done <- r.ProjectBatchNow(ctx, "tenant", ids) }()
	deadline := time.After(time.Second)
	for len(r.lanes[0].gate) == 0 {
		select {
		case <-deadline:
			t.Fatal("batch did not acquire first lane")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case results := <-done:
		for _, err := range results {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("batch did not cancel")
	}
	<-r.lanes[1].gate
	for i := range r.lanes {
		if len(r.lanes[i].gate) != 0 || r.lanes[i].urgent.Load() != 0 {
			t.Fatal("lane leaked", i)
		}
	}
}

func TestOverlappingCloseProjectionBatchesDoNotDeadlock(t *testing.T) {
	r := NewRuntime(config.KACDeliveryConfig{ProjectionConcurrency: 4}, nil, "stream", readerFunc(testReader), writerStub{write: func(_ context.Context, alerts []domain.Alert, _ bool) []WriteResult {
		return make([]WriteResult, len(alerts))
	}}, nil, nil, nil)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			ids := []string{"a", "b", "c", "d"}
			if i%2 == 0 {
				ids = []string{"d", "c", "b", "a"}
			}
			for range 10 {
				for _, err := range r.ProjectBatchNow(ctx, "tenant", ids) {
					if err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	workers.Wait()
}
