// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func lifecycleTestRecord(t *testing.T, replica string) (*lifecycleRecord, config.Config, redis.UniversalClient) {
	t.Helper()
	address, reader := startPhaseTwoRedis(t)
	cfg := config.Default()
	cfg.Redis.Address = address
	cfg.PhaseTwo.Worker.ID = replica
	record := newLifecycleRecord(cfg)
	t.Cleanup(record.close)
	// Read on a client of the test's own: the application closes the
	// record's when it stops.
	return record, cfg, reader
}

// Starts and stops outlive the process, read newest first and summed by
// replica: a clean stop by its reason, and a start straight after a start
// of the same name as an exit that left no stop. The record is bounded.
func TestTheLifecycleRecordOutlivesTheProcess(t *testing.T) {
	record, cfg, reader := lifecycleTestRecord(t, "pod-a")
	at := time.Date(2026, 9, 24, 6, 0, 0, 0, time.UTC)
	record.now = func() time.Time { at = at.Add(time.Second); return at }
	record.start()
	record.stop(lifecycleStopSignal, nil)
	other := *record
	other.replica = "pod-b"
	other.start()
	other.start() // pod-b's first process died without a stop
	other.stop(lifecycleStopWorkerStopped, errors.New("the worker stopped: "+strings.Repeat("x", 2*lifecycleErrorBytes)))

	reading, err := readLifecycle(context.Background(), reader, lifecycleRecordKey(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(reading.Entries) != 5 || reading.Entries[0].Replica != "pod-b" || reading.Entries[0].Event != "stop" ||
		len(reading.Entries[0].Error) != lifecycleErrorBytes || reading.Entries[0].Build == "" {
		t.Fatalf("entries %+v, want five, newest first, the error bounded", reading.Entries)
	}
	byReplica := map[string]lifecycleReplica{}
	for _, summary := range reading.Replicas {
		byReplica[summary.Replica] = summary
	}
	if a := byReplica["pod-a"]; a.UncleanRestarts != 0 || a.LastEvent != "stop" || a.Stops[lifecycleStopSignal] != 1 {
		t.Fatalf("pod-a %+v", a)
	}
	if b := byReplica["pod-b"]; b.UncleanRestarts != 1 || b.LastEvent != "stop" || b.Stops[lifecycleStopWorkerStopped] != 1 {
		t.Fatalf("pod-b %+v, want one unclean restart", b)
	}
	for i := 0; i < lifecycleRecordEntries+10; i++ {
		record.start()
	}
	// Bounded in the store, not only in the read.
	if kept := reader.LLen(context.Background(), lifecycleRecordKey(cfg)).Val(); kept != lifecycleRecordEntries {
		t.Fatalf("%d entries kept, want %d", kept, lifecycleRecordEntries)
	}
	if ttl := reader.TTL(context.Background(), lifecycleRecordKey(cfg)).Val(); ttl <= 0 || ttl > lifecycleRecordTTL {
		t.Fatalf("ttl %v", ttl)
	}
}

// Why the application stopped, from how its run ended.
func TestTheStopReasonFollowsHowTheRunEnded(t *testing.T) {
	for _, tc := range []struct {
		signalled, bundle, http bool
		want                    string
	}{
		{true, false, false, lifecycleStopSignal},
		{false, true, false, lifecycleStopWorkerStopped},
		{false, false, true, lifecycleStopHTTPStopped},
	} {
		if got := lifecycleStopReason(tc.signalled, tc.bundle, tc.http); got != tc.want {
			t.Errorf("signalled %v bundle %v http %v: %s, want %s", tc.signalled, tc.bundle, tc.http, got, tc.want)
		}
	}
}

// The application writes its start and, on a signal, its stop.
func TestTheApplicationWritesItsStartAndStop(t *testing.T) {
	record, rcfg, reader := lifecycleTestRecord(t, "pod-app")
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)
	listener := &fakeHTTPRuntime{}
	listener.run = func(ctx context.Context, _ string, _ time.Duration) error { <-ctx.Done(); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	dependencies := phaseTwoApplicationDependencies{
		configureCPU: func() (string, error) { return "cpu_quota", nil },
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource, httpSurface) (httpRuntime, error) {
			return listener, nil
		},
		lifecycle: func(config.Config) *lifecycleRecord { return record },
	}
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), dependencies)
	}()
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reading, err := readLifecycle(context.Background(), reader, lifecycleRecordKey(rcfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(reading.Entries) != 2 || reading.Entries[1].Event != "start" || reading.Entries[0].Event != "stop" ||
		reading.Entries[0].Reason != lifecycleStopSignal || reading.Entries[0].Replica != "pod-app" {
		t.Fatalf("entries %+v, want a start then a stop on the signal", reading.Entries)
	}
}
