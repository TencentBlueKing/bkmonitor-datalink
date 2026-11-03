// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package taskdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type releaseFailureDocuments struct {
	emptySources
	records []eventsource.Record
	failID  string
	listErr error
	reads   []string
}

func (d *releaseFailureDocuments) List(_ context.Context, _ string, after string, limit int) ([]json.RawMessage, error) {
	if d.listErr != nil {
		return nil, d.listErr
	}
	var result []json.RawMessage
	for _, r := range d.records {
		if r.ID <= after {
			continue
		}
		b, _ := json.Marshal(r)
		result = append(result, b)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (d *releaseFailureDocuments) Get(_ context.Context, kind, id string) (json.RawMessage, string, error) {
	if kind == "records" {
		return nil, "", errors.New("recovery unavailable")
	}
	d.reads = append(d.reads, id)
	if id == d.failID+":1" {
		return nil, "", eventsource.ErrNotFound
	}
	for _, r := range d.records {
		if id == r.ID+":1" {
			b, _ := json.Marshal(eventsource.Release{ID: r.ID, Version: 1, Spec: r.Spec, Deleted: r.Deleted})
			return b, "", nil
		}
	}
	return nil, "", eventsource.ErrNotFound
}

type releaseFailureObserver struct {
	noopObserver
	failures   []string
	operations []string
}

func (o *releaseFailureObserver) SourceReleaseFailed(_ context.Context, id, operation string, _ error) {
	o.failures = append(o.failures, id+":"+operation)
}

func (o *releaseFailureObserver) Operation(_ context.Context, operation string, success bool, _ time.Duration) {
	if !success {
		o.operations = append(o.operations, operation)
	}
}

func TestReleaseFailureIsolatedAcrossPages(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("recover=%t", recovery), func(t *testing.T) {
			docs := &releaseFailureDocuments{failID: "source-099"}
			for i := 0; i < 101; i++ {
				r := eventsource.Record{ID: fmt.Sprintf("source-%03d", i), Published: 1}
				if r.ID == docs.failID && recovery {
					r.Pending = &eventsource.Release{Version: 2}
				}
				docs.records = append(docs.records, r)
			}
			observer := &releaseFailureObserver{}
			c := &Controller{sources: eventsource.New(docs, config.SeverityConfig{}), observer: observer}
			releases, unavailable, err := c.releases(t.Context())
			if err != nil || len(releases) != 100 || !unavailable[docs.failID] || releases[99].ID != "source-100" {
				t.Fatalf("releases=%d unavailable=%v err=%v", len(releases), unavailable, err)
			}
			operation := "read"
			if recovery {
				operation = "recover"
			}
			if len(observer.failures) != 1 || observer.failures[0] != docs.failID+":"+operation || len(observer.operations) != 1 || observer.operations[0] != "source_release_"+operation {
				t.Fatalf("observations: %+v", observer)
			}
			docs.failID = ""
			docs.records[99].Pending = nil
			releases, unavailable, err = c.releases(t.Context())
			if err != nil || len(releases) != 101 || len(unavailable) != 0 {
				t.Fatalf("recovery: releases=%d unavailable=%v err=%v", len(releases), unavailable, err)
			}
		})
	}
}

func TestReleaseListFailureAndCancellationAbortRound(t *testing.T) {
	sentinel := errors.New("list failed")
	docs := &releaseFailureDocuments{listErr: sentinel}
	c := &Controller{sources: eventsource.New(docs, config.SeverityConfig{}), observer: noopObserver{}}
	if _, _, err := c.releases(t.Context()); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	docs.listErr = nil
	docs.failID = "bad"
	docs.records = []eventsource.Record{{ID: "bad", Published: 1}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := c.releases(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestUnavailableReleasePreservesTasksWhileHealthySourcesPlan(t *testing.T) {
	state, release, now := fixture()
	for _, entry := range []struct {
		id      string
		expires time.Time
	}{
		{"bad", now.Add(time.Minute)}, {"expired", now.Add(-LeaseTTL - SafetyMargin)}, {"removed", now.Add(time.Minute)},
	} {
		state.Tasks[entry.id] = Task{ID: entry.id, Source: entry.id, Role: "cleaner", Phase: "running", Expires: entry.expires, Worker: "0", Epoch: 1}
	}
	reconcile(&state, []eventsource.Release{release}, map[string]bool{"bad": true, "expired": true}, now)
	if state.Tasks["bad"].Phase != "running" || state.Tasks["expired"].Phase != "stopped" || state.Tasks["removed"].Phase != "stopping" {
		t.Fatalf("tasks: %+v", state.Tasks)
	}
	healthy := 0
	for _, task := range state.Tasks {
		if task.Source == release.ID {
			healthy++
		}
	}
	if healthy == 0 {
		t.Fatal("healthy source was not planned")
	}
	// 读取恢复后，真实删除仍应撤销旧任务。
	reconcile(&state, nil, nil, now)
	if state.Tasks["bad"].Phase != "stopping" {
		t.Fatal("missing source retained after recovery")
	}
}
