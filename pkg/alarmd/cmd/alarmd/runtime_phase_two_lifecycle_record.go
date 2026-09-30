// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// A process's exit leaves no trace a reader can reach once its Pod is gone:
// the kubelet keeps the last termination only while the Pod lives, and a
// rollout or an eviction deletes it. So every process writes a start and,
// when it can, a stop with why it stopped, into a short list in the runtime
// store. A stop never written -- OOM, SIGKILL -- shows as a start with no
// stop after it, and a second start under the same replica name (the
// container restarted in place) as an unclean exit between them.
//
// Best effort, bounded and on a connection of its own: the record must
// never delay a start or a shutdown, and it is not read by anything that
// decides.
const (
	lifecycleRecordEntries = 64
	lifecycleRecordTTL     = 30 * 24 * time.Hour
	lifecycleWriteTimeout  = 2 * time.Second
	lifecycleErrorBytes    = 512
)

// The stop reasons, closed.
const (
	lifecycleStopSignal        = "signal"
	lifecycleStopWorkerStopped = "worker_stopped"
	lifecycleStopHTTPStopped   = "http_stopped"
	lifecycleStopStartFailed   = "start_failed"
)

// lifecycleEntry is one start or stop.
type lifecycleEntry struct {
	Replica string    `json:"replica"`
	Event   string    `json:"event"`
	At      time.Time `json:"at"`
	Build   string    `json:"build"`
	Reason  string    `json:"reason,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type lifecycleRecord struct {
	client  redis.UniversalClient
	key     string
	replica string
	build   string
	now     func() time.Time
}

func lifecycleRecordKey(cfg config.Config) string {
	return productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "lifecycle")
}

// newLifecycleRecord opens the record's own connection. Nil when there is
// no runtime store to write to; every method is safe on nil.
func newLifecycleRecord(cfg config.Config) *lifecycleRecord {
	connection := cfg.RuntimeStoreRedis()
	if connection.Address == "" && len(connection.SentinelAddress) == 0 {
		return nil
	}
	options := observationRedisOptions(connection)
	options.PoolSize, options.MinIdleConns = 1, 0
	return &lifecycleRecord{client: redis.NewUniversalClient(options), key: lifecycleRecordKey(cfg),
		replica: cfg.PhaseTwo.Worker.ID, build: version + "/" + commit, now: time.Now}
}

func (record *lifecycleRecord) start() {
	record.write(lifecycleEntry{Event: "start"})
}

// stop records why the process stopped. err is the error it stops with,
// when there is one.
func (record *lifecycleRecord) stop(reason string, err error) {
	entry := lifecycleEntry{Event: "stop", Reason: reason}
	if err != nil {
		text := err.Error()
		if len(text) > lifecycleErrorBytes {
			text = text[:lifecycleErrorBytes]
		}
		entry.Error = text
	}
	record.write(entry)
}

func (record *lifecycleRecord) write(entry lifecycleEntry) {
	if record == nil {
		return
	}
	entry.Replica, entry.Build, entry.At = record.replica, record.build, record.now().UTC()
	encoded, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), lifecycleWriteTimeout)
	defer cancel()
	_, _ = record.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, record.key, encoded)
		pipe.LTrim(ctx, record.key, 0, lifecycleRecordEntries-1)
		pipe.Expire(ctx, record.key, lifecycleRecordTTL)
		return nil
	})
}

func (record *lifecycleRecord) close() {
	if record != nil {
		_ = record.client.Close()
	}
}

// lifecycleStopReason is why the application is stopping, from how its run
// ended: the context it was given, or one of its components stopping on
// its own.
func lifecycleStopReason(signalled, bundleStoppedEarly, httpStoppedEarly bool) string {
	switch {
	case bundleStoppedEarly:
		return lifecycleStopWorkerStopped
	case httpStoppedEarly:
		return lifecycleStopHTTPStopped
	case signalled:
		return lifecycleStopSignal
	}
	return lifecycleStopSignal
}

// lifecycleReplica sums one replica's entries: its last event, and the
// exits it left no stop for.
type lifecycleReplica struct {
	Replica   string    `json:"replica"`
	LastEvent string    `json:"last_event"`
	LastAt    time.Time `json:"last_at"`
	// UncleanRestarts counts starts that followed a start of the same
	// replica name with no stop between: the process before it died without
	// writing one (OOM, SIGKILL) and the container was started again.
	UncleanRestarts int `json:"unclean_restarts"`
	// Stops is how many stops were written, by reason.
	Stops map[string]int `json:"stops,omitempty"`
}

type lifecycleReading struct {
	Entries  []lifecycleEntry   `json:"entries"`
	Replicas []lifecycleReplica `json:"replicas"`
	// Unreadable counts entries that did not decode.
	Unreadable int `json:"unreadable,omitempty"`
}

// readLifecycle reads the record, newest first, and sums it by replica.
func readLifecycle(ctx context.Context, client redis.UniversalClient, key string) (lifecycleReading, error) {
	raw, err := client.LRange(ctx, key, 0, lifecycleRecordEntries-1).Result()
	if err != nil {
		return lifecycleReading{}, err
	}
	reading := lifecycleReading{Entries: []lifecycleEntry{}, Replicas: []lifecycleReplica{}}
	for _, text := range raw {
		var entry lifecycleEntry
		if json.Unmarshal([]byte(text), &entry) != nil {
			reading.Unreadable++
			continue
		}
		reading.Entries = append(reading.Entries, entry)
	}
	byReplica := map[string]*lifecycleReplica{}
	var order []string
	// Oldest first, so "a start after a start" reads in time order.
	for i := len(reading.Entries) - 1; i >= 0; i-- {
		entry := reading.Entries[i]
		summary := byReplica[entry.Replica]
		if summary == nil {
			summary = &lifecycleReplica{Replica: entry.Replica}
			byReplica[entry.Replica] = summary
			order = append(order, entry.Replica)
		}
		if entry.Event == "start" && summary.LastEvent == "start" {
			summary.UncleanRestarts++
		}
		if entry.Event == "stop" {
			if summary.Stops == nil {
				summary.Stops = map[string]int{}
			}
			summary.Stops[entry.Reason]++
		}
		summary.LastEvent, summary.LastAt = entry.Event, entry.At
	}
	for _, replica := range order {
		reading.Replicas = append(reading.Replicas, *byReplica[replica])
	}
	return reading, nil
}
