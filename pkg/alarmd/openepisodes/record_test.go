// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openepisodes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

const prefix = "alarmd-test"

var key = openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"}

func fp(n int) string { return fmt.Sprintf("%032x", n) }

// envelope is what a worker acknowledges for series n of strategy 41 at
// revision 7, whose Plan's target identity is IP and cloud area.
func envelope(kind string, n int) contract.TriggerEventV1 {
	return contract.TriggerEventV1{EventKind: kind, TenantID: "tenant-a", DedupeMD5: fp(n),
		PlanRef:          contract.RuntimePlanRefV1{StrategyID: "41"},
		StrategyRef:      &contract.StrategySnapshotRef{TenantID: "tenant-a", StrategyID: 41, Revision: 7},
		TargetDimensions: []string{"bk_target_ip", "bk_target_cloud_id"},
		RecordRef: contract.TriggerRecordRefV1{Dimensions: map[string]json.RawMessage{
			"bk_target_ip": json.RawMessage(fmt.Sprintf(`"192.0.2.%d"`, n)), "bk_target_cloud_id": json.RawMessage(`0`),
			"device": json.RawMessage(`"sda"`)}}}
}

// An ABNORMAL is an entry at its strategy revision with the series'
// target values as the link indexes them (an integer as its decimal
// text); a series missing a target dimension has no target; a RECOVERY is
// a removal; an envelope the link never sees is nothing.
func TestChangeOfReadsTheEnvelopeAsTheLinkFilesIt(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	change, ok := openepisodes.ChangeOf(envelope(contract.TriggerEventAbnormal, 1), at)
	if !ok || !change.Open || change.Key != key || change.Fingerprint != fp(1) || change.Entry.Version != "7" ||
		change.Entry.OpenedAt != at.Unix() || change.Entry.Target["bk_target_ip"] != "192.0.2.1" || change.Entry.Target["bk_target_cloud_id"] != "0" ||
		len(change.Entry.Target) != 2 {
		t.Fatalf("ABNORMAL: %+v, %v", change, ok)
	}
	partial := envelope(contract.TriggerEventAbnormal, 2)
	delete(partial.RecordRef.Dimensions, "bk_target_cloud_id")
	if change, ok := openepisodes.ChangeOf(partial, at); !ok || change.Entry.Target != nil {
		t.Fatalf("a series without every target dimension: %+v", change)
	}
	if change, ok := openepisodes.ChangeOf(envelope(contract.TriggerEventRecovery, 1), at); !ok || change.Open {
		t.Fatalf("RECOVERY: %+v, %v", change, ok)
	}
	legacy := envelope(contract.TriggerEventAbnormal, 3)
	legacy.LegacyOutput = &contract.LegacyEventContext{}
	unfingerprinted := envelope(contract.TriggerEventAbnormal, 4)
	unfingerprinted.DedupeMD5 = ""
	for name, event := range map[string]contract.TriggerEventV1{"legacy": legacy, "no fingerprint": unfingerprinted} {
		if _, ok := openepisodes.ChangeOf(event, at); ok {
			t.Fatalf("%s made a change", name)
		}
	}
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

type noSearch struct{}

func (noSearch) Search(context.Context, string, json.RawMessage, string, int) (linkdmanage.SearchPage, error) {
	return linkdmanage.SearchPage{}, nil
}

// worker is the production pair on a worker's ACK path: the copy judges
// which envelopes change the record, the writer queues and writes them.
type worker struct {
	copy   *activecopy.Copy
	writer *openepisodes.Writer
}

func newWorker(t *testing.T, client redis.UniversalClient, clk *clock) worker {
	t.Helper()
	copy, err := activecopy.New(activecopy.Options{Link: noSearch{}, EventSourceID: "built_in_bk", Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	store, err := openepisodes.NewStore(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := openepisodes.NewWriter(store, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	return worker{copy: copy, writer: writer}
}

// acknowledge is one acked batch, written before it returns.
func (w worker) acknowledge(t *testing.T, events ...contract.TriggerEventV1) {
	t.Helper()
	w.writer.Acknowledged(events, w.copy.Acknowledged(events))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.writer.Run(ctx); close(done) }()
	testwait.Until(t, "the episode writer drains", func() bool { return w.writer.Pending() == 0 })
	cancel()
	<-done
}

func load(t *testing.T, client redis.UniversalClient) openepisodes.Record {
	t.Helper()
	store, _ := openepisodes.NewStore(client, prefix)
	record, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// The record holds each open episode once: the first ABNORMAL writes it,
// a repeat writes nothing, a RECOVERY removes it, and a restarted worker
// writes each open episode once more (the same entry). The hash's life is
// set on every write, and once a RefreshEvery for a strategy that only
// repeats.
func TestTheRecordHoldsEachOpenEpisodeOnce(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	client := server.Client()
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	w := newWorker(t, client, clk)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 1), envelope(contract.TriggerEventAbnormal, 2))
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 1), envelope(contract.TriggerEventAbnormal, 2))
	if stats := w.writer.Stats(); stats[openepisodes.OutcomeOpened] != 2 {
		t.Fatalf("a repeated ABNORMAL wrote: %v", stats)
	}
	record := load(t, client)
	if !record.Exists || len(record.Entries) != 2 || record.Entries[0].Version != "7" {
		t.Fatalf("record %+v", record)
	}
	if ttl := client.TTL(context.Background(), prefix+":openepisodes:v1:tenant-a:41").Val(); ttl <= openepisodes.TTL-time.Minute || ttl > openepisodes.TTL {
		t.Fatalf("ttl %v", ttl)
	}
	w.acknowledge(t, envelope(contract.TriggerEventRecovery, 1))
	if record := load(t, client); len(record.Entries) != 1 || record.Entries[0].Fingerprint != fp(2) {
		t.Fatalf("after the RECOVERY: %+v", record)
	}
	// A strategy that only repeats: its life is set again once a
	// RefreshEvery, not each batch.
	client.Expire(context.Background(), prefix+":openepisodes:v1:tenant-a:41", time.Hour)
	clk.at = clk.at.Add(openepisodes.RefreshEvery - time.Second)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 2))
	if ttl := client.TTL(context.Background(), prefix+":openepisodes:v1:tenant-a:41").Val(); ttl > time.Hour {
		t.Fatalf("refreshed before RefreshEvery: %v", ttl)
	}
	clk.at = clk.at.Add(time.Second)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 2))
	if ttl := client.TTL(context.Background(), prefix+":openepisodes:v1:tenant-a:41").Val(); ttl <= time.Hour {
		t.Fatalf("not refreshed after RefreshEvery: %v", ttl)
	}
	if stats := w.writer.Stats(); stats[openepisodes.OutcomeRefreshed] != 1 || stats[openepisodes.OutcomeEnded] != 1 {
		t.Fatalf("stats %v", stats)
	}
	// The last RECOVERY empties the hash of entries; its start stays.
	restarted := newWorker(t, client, clk)
	restarted.acknowledge(t, envelope(contract.TriggerEventAbnormal, 2))
	if stats := restarted.writer.Stats(); stats[openepisodes.OutcomeOpened] != 1 {
		t.Fatalf("a restarted worker did not write its open episode: %v", stats)
	}
	restarted.acknowledge(t, envelope(contract.TriggerEventRecovery, 2))
	if record := load(t, client); !record.Exists || len(record.Entries) != 0 || record.Since.IsZero() {
		t.Fatalf("record after every RECOVERY: %+v", record)
	}
}

// A Redis that does not take the pipeline is counted per change and never
// blocks the ACK path; a full queue drops and counts, never waits.
func TestAFailedWriteAndAFullQueueAreCounted(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	w := newWorker(t, client, clk)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 1), envelope(contract.TriggerEventRecovery, 2))
	if stats := w.writer.Stats(); stats[openepisodes.OutcomeWriteFailed] != 2 || stats[openepisodes.OutcomeOpened] != 0 {
		t.Fatalf("stats %v", stats)
	}
	// Nothing drains the queue now: QueueBatches batches fill it, the next
	// is dropped without waiting.
	started := time.Now()
	for i := 0; i <= openepisodes.QueueBatches; i++ {
		event := envelope(contract.TriggerEventRecovery, 100+i)
		w.writer.Acknowledged(nil, []contract.TriggerEventV1{event})
	}
	if stats := w.writer.Stats(); stats[openepisodes.OutcomeQueueFull] != 1 || time.Since(started) > time.Second {
		t.Fatalf("stats %v after %v", stats, time.Since(started))
	}
}

// The hash's start is written once, by the write that creates the hash: a
// later write does not move it, the entries' count does not include it,
// and a hash lost and created again starts at its new first write.
func TestTheRecordStartsAtItsFirstWrite(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	client := server.Client()
	start := time.Unix(1_800_000_000, 0)
	clk := &clock{at: start}
	w := newWorker(t, client, clk)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 1))
	clk.at = start.Add(time.Hour)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 2))
	store, _ := openepisodes.NewStore(client, prefix)
	if record := load(t, client); !record.Since.Equal(start) || len(record.Entries) != 2 {
		t.Fatalf("record %+v, want started at %v with two entries", record, start)
	}
	if since, ok, err := store.Since(context.Background(), key); err != nil || !ok || !since.Equal(start) {
		t.Fatalf("since %v %v %v", since, ok, err)
	}
	client.Del(context.Background(), prefix+":openepisodes:v1:tenant-a:41")
	if _, ok, err := store.Since(context.Background(), key); err != nil || ok {
		t.Fatalf("a lost record still has a start (%v)", err)
	}
	clk.at = start.Add(2 * time.Hour)
	w.acknowledge(t, envelope(contract.TriggerEventAbnormal, 3))
	if record := load(t, client); !record.Since.Equal(clk.at) || len(record.Entries) != 1 {
		t.Fatalf("record %+v, want started again at %v", record, clk.at)
	}
}

// A worker restarted after its strategy moved to a new version sees the
// still-open episode's ABNORMAL as new (its copy is empty) and writes it
// again: the entry keeps the version and opening it was written with, as
// the link keeps the alert's labels from its creation. After a RECOVERY
// the next opening is written at its own version.
func TestARepeatAfterARestartKeepsTheEpisodesVersion(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	client := server.Client()
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	newWorker(t, client, clk).acknowledge(t, envelope(contract.TriggerEventAbnormal, 1))
	clk.at = clk.at.Add(time.Hour)
	moved := envelope(contract.TriggerEventAbnormal, 1)
	moved.StrategyRef = &contract.StrategySnapshotRef{TenantID: "tenant-a", StrategyID: 41, Revision: 8}
	restarted := newWorker(t, client, clk)
	restarted.acknowledge(t, moved)
	if record := load(t, client); len(record.Entries) != 1 || record.Entries[0].Version != "7" ||
		record.Entries[0].OpenedAt != 1_800_000_000 {
		t.Fatalf("record %+v, want the episode at version 7 as it opened", record)
	}
	restarted.acknowledge(t, envelope(contract.TriggerEventRecovery, 1))
	restarted.acknowledge(t, moved)
	if record := load(t, client); len(record.Entries) != 1 || record.Entries[0].Version != "8" {
		t.Fatalf("record %+v, want the new episode at version 8", record)
	}
}
