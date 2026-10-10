// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// realRedis starts a redis-server for the test and returns its address and
// a client of it.
func realRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	executable := redistest.Server(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no",
		"--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = client.Close() })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if client.Ping(context.Background()).Err() == nil {
			return address, client
		}
		if time.Now().After(deadline) {
			t.Fatalf("redis-server did not become ready: %s", output.String())
		}
	}
}

// summarizedSnapshot is a replica's snapshot with rows, and more owned
// objects than a small budget lets its list carry.
func summarizedSnapshot(replica string, owned int) Snapshot {
	snapshot := snapshotsWithAnomalies(2)[0]
	snapshot.Replica, snapshot.TakenAt = replica, now
	snapshot.OwnedObjects = nil
	for n := 0; n < owned; n++ {
		snapshot.OwnedObjects = append(snapshot.OwnedObjects, replica+"-object-"+string(rune('a'+n%26))+string(rune('a'+n/26)))
	}
	snapshot.Owned, snapshot.Determined = owned, owned
	for index := range snapshot.Anomalies {
		snapshot.Anomalies[index].Replica = replica
	}
	return snapshot
}

// A summarized publish writes the snapshot, its summary and its whole owned
// list, each readable for the store's TTL: the summary is the one the reader
// would make from the snapshot as written, and the owned list is the whole
// set though the snapshot's own list was cut to its budget.
func TestASummarizedPublishWritesTheSnapshotItsSummaryAndItsWholeOwnedList(t *testing.T) {
	_, client := realRedis(t)
	store := mustStore(t, client, time.Minute, 256)
	meter := &storeMeterRecord{}
	store.Meter(meter)
	snapshot := summarizedSnapshot("pod-a", 40)
	if _, err := store.PublishSummarized(context.Background(), snapshot, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	written, err := store.Load(context.Background(), []string{"pod-a"})
	if err != nil || len(written) != 1 || len(written[0].OwnedObjects) >= len(snapshot.OwnedObjects) {
		t.Fatalf("snapshot %+v error %v, want it written with its owned list cut to the budget", written, err)
	}
	summaries, err := store.LoadSummaries(context.Background(), []string{"pod-a", "pod-gone"})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries %+v error %v, want pod-a's alone", summaries, err)
	}
	sameJSON(t, "summary", summaries[0], SummaryOf(written[0], snapshot.OwnedObjects, 10*time.Minute))
	if summaries[0].Owned != DigestOf(snapshot.OwnedObjects) {
		t.Fatalf("owned digest %+v, want the whole set's", summaries[0].Owned)
	}
	owned, err := store.LoadOwned(context.Background(), []string{"pod-a", "pod-gone"})
	if err != nil || len(owned) != 1 || len(owned["pod-a"]) != len(snapshot.OwnedObjects) {
		t.Fatalf("owned %v error %v, want pod-a's whole list alone", owned, err)
	}
	for _, key := range []string{store.snapshotKey("pod-a"), store.summaryKey("pod-a"), store.ownedKey("pod-a")} {
		if ttl := client.TTL(context.Background(), key).Val(); ttl <= 0 || ttl > time.Minute {
			t.Errorf("%s ttl %v, want the store's", key, ttl)
		}
	}
	summary := client.Get(context.Background(), store.summaryKey("pod-a")).Val()
	if len(meter.summaries) != 1 || meter.summaries[0] != len(summary) || len(meter.summaryLoads) != 1 ||
		meter.summaryLoads[0] != [2]int{1, len(summary)} || len(meter.ownedLoads) != 1 || meter.ownedLoads[0][0] != 1 {
		t.Fatalf("meter %+v, want the summary's bytes written and each read counted", meter)
	}
}

// The strategies a replica's objects evaluate are counted into the summary
// it writes and are not written with its snapshot: no reader of a stored
// snapshot needs them, and they would grow every write by one entry per
// strategy.
func TestASummarizedPublishCountsTheEvaluatingStrategiesWithoutWritingThem(t *testing.T) {
	_, client := realRedis(t)
	store := mustStore(t, client, time.Minute, 0)
	snapshot := summarizedSnapshot("pod-a", 2)
	snapshot.EvaluatingStrategies = []StrategyRef{{StrategyID: "854", BusinessID: "2"}, {StrategyID: "900", BusinessID: "2"}}
	snapshot.EvaluatingStrategiesKnown = true
	summary, err := store.PublishSummarized(context.Background(), snapshot, 10*time.Minute)
	if err != nil || summary.Part.RunningStrategies == nil {
		t.Fatalf("summary %+v error %v, want the running strategies counted", summary.Part, err)
	}
	raw := client.Get(context.Background(), store.snapshotKey("pod-a")).Val()
	if raw == "" || strings.Contains(raw, "evaluating_strategies") {
		t.Fatalf("snapshot written as %s, want it without the strategies list", raw)
	}
	stored := client.Get(context.Background(), store.summaryKey("pod-a")).Val()
	if !strings.Contains(stored, `"running_strategies"`) {
		t.Fatalf("summary written as %s, want the running strategies in it", stored)
	}
	written, err := store.Load(context.Background(), []string{"pod-a"})
	if err != nil || len(written) != 1 || written[0].EvaluatingStrategiesKnown || written[0].EvaluatingStrategies != nil {
		t.Fatalf("snapshot read back %+v error %v, want it saying nothing of its strategies", written, err)
	}
}

// execCutter is a proxy to Redis that lets a transaction's MULTI and
// commands through, waits until Redis has queued three of them, and then
// drops the connection instead of passing EXEC on.
func execCutter(t *testing.T, target string) (string, func() bool) {
	t.Helper()
	var cutQueued sync.WaitGroup
	var mu sync.Mutex
	queuedAll := false
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			cutQueued.Add(1)
			go func() {
				defer cutQueued.Done()
				if cutBeforeExec(client, target) {
					mu.Lock()
					queuedAll = true
					mu.Unlock()
				}
			}()
		}
	}()
	return listener.Addr().String(), func() bool {
		cutQueued.Wait()
		mu.Lock()
		defer mu.Unlock()
		return queuedAll
	}
}

// cutBeforeExec proxies one connection and says whether Redis had queued
// three commands when it was cut.
func cutBeforeExec(client net.Conn, target string) bool {
	defer client.Close()
	server, err := net.Dial("tcp", target)
	if err != nil {
		return false
	}
	defer server.Close()
	var mu sync.Mutex
	queued := 0
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := server.Read(buffer)
			if n > 0 {
				mu.Lock()
				queued += bytes.Count(buffer[:n], []byte("+QUEUED"))
				mu.Unlock()
				_, _ = client.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	var pending []byte
	buffer := make([]byte, 64<<10)
	for {
		n, err := client.Read(buffer)
		pending = append(pending, buffer[:n]...)
		if frame := bytes.Index(bytes.ToLower(pending), []byte("*1\r\n$4\r\nexec\r\n")); frame >= 0 {
			_, _ = server.Write(pending[:frame])
			for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				mu.Lock()
				done := queued >= 3
				mu.Unlock()
				if done {
					return true
				}
			}
			return false
		}
		if err != nil {
			_, _ = server.Write(pending)
			return false
		}
	}
}

// A connection lost before EXEC leaves all three keys as the publish before
// left them: Redis had the new snapshot, summary and owned list queued and
// dropped them together, so no reader finds a summary beside a snapshot it
// was not made from.
func TestASummarizedPublishCutBeforeExecLeavesAllThreeAsTheyWere(t *testing.T) {
	address, client := realRedis(t)
	before := mustStore(t, client, time.Minute, 0)
	if _, err := before.PublishSummarized(context.Background(), summarizedSnapshot("pod-a", 3), 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	keys := []string{before.snapshotKey("pod-a"), before.summaryKey("pod-a"), before.ownedKey("pod-a")}
	old := client.MGet(context.Background(), keys...).Val()

	proxy, queued := execCutter(t, address)
	cut := redis.NewClient(&redis.Options{Addr: proxy, DialTimeout: time.Second, ReadTimeout: 5 * time.Second, MaxRetries: -1})
	t.Cleanup(func() { _ = cut.Close() })
	next := summarizedSnapshot("pod-a", 5)
	next.TakenAt = now.Add(time.Minute)
	if _, err := mustStore(t, cut, time.Minute, 0).PublishSummarized(context.Background(), next, 10*time.Minute); err == nil {
		t.Fatal("a publish whose EXEC never arrived reported success")
	}
	_ = cut.Close()
	if !queued() {
		t.Fatal("Redis had not queued the three writes when the connection was cut: the test proves nothing")
	}
	after := client.MGet(context.Background(), keys...).Val()
	for index := range keys {
		if after[index] != old[index] {
			t.Errorf("%s changed though its transaction never ran", keys[index])
		}
	}
	// The same publish straight to Redis replaces all three: the cut is what
	// kept them.
	if _, err := before.PublishSummarized(context.Background(), next, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	for index, value := range client.MGet(context.Background(), keys...).Val() {
		if value == old[index] {
			t.Errorf("%s unchanged by a publish that ran", keys[index])
		}
	}
}

// The service reads a replica that published a summary from it, and one
// that did not -- an older build during a rollout -- from its snapshot, at
// the snapshot's own moment, and the answer is the one every snapshot gives.
// The owned lists are read only when the digests disagree.
func TestTheServiceSummarizesAReplicaThatPublishedNoSummaryFromItsSnapshot(t *testing.T) {
	_, client := realRedis(t)
	store := mustStore(t, client, time.Minute, 0)
	meter := &storeMeterRecord{}
	store.Meter(meter)
	current, older := summarizedSnapshot("pod-a", 3), summarizedSnapshot("pod-b", 2)
	if _, err := store.PublishSummarized(context.Background(), current, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	expected := append(append([]string(nil), current.OwnedObjects...), older.OwnedObjects...)
	for name, ids := range map[string][]string{"agreeing": expected, "disagreeing": append(expected[1:], "nobody-holds-this")} {
		meter.ownedLoads, meter.summaryLoads = nil, nil
		expectation := Expectation{QueryGroups: len(ids), Known: true, IDs: ids}
		service := mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicas()}, store)
		view, part := service.Summarized(context.Background(), 10*time.Minute)
		snapshots, err := store.Load(context.Background(), replicas())
		if err != nil {
			t.Fatal(err)
		}
		want := healthFromSnapshots(Aggregate(expectation, snapshots, replicas(), now, freshness), now, 10*time.Minute)
		sameJSON(t, name, healthOf(&view, part, now), want)
		// pod-b's list came from its snapshot; only pod-a's is read, and only
		// when the digests disagree.
		if read := len(meter.ownedLoads) == 1; read != (name == "disagreeing") || len(meter.summaryLoads) != 1 {
			t.Errorf("%s: owned reads %v, summary reads %v", name, meter.ownedLoads, meter.summaryLoads)
		}
	}
}

// A summary decodes into what was written, and the summary that decodes
// encodes to the same bytes: the strategies of the impact carried as an
// ordered list, not dropped with the set's struct keys.
func TestASummaryReadsBackAsWritten(t *testing.T) {
	for _, snapshot := range partReplicas() {
		summary := SummaryOf(snapshot, nil, 10*time.Minute)
		encoded, err := json.Marshal(summary)
		if err != nil {
			t.Fatal(err)
		}
		var read ReplicaSummary
		if err := json.Unmarshal(encoded, &read); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(read)
		if !bytes.Equal(encoded, again) {
			t.Fatalf("%s: read back as\n%s\nwritten as\n%s", snapshot.Replica, again, encoded)
		}
		strategies := 0
		for _, part := range read.Part.Impact.Parts {
			strategies += len(part.Strategies)
		}
		if strategies == 0 || len(read.Part.CohortRows) == 0 || read.Part.PrunedSkipsTotal == 0 {
			t.Fatalf("%s: the fixture carries no strategies, cohorts or lists: %s", snapshot.Replica, encoded)
		}
	}
}

// Every list a snapshot carries a row or an object per entry of is left out
// of its head; every other list is kept. A list added to the snapshot later
// fails here until it is put on one side: a row list kept in the head grows
// every summary with the rows it exists to leave behind.
func TestEveryListOfASnapshotIsEitherRowsOrKeptInItsHead(t *testing.T) {
	rows := map[string]bool{"OwnedObjects": true, "Anomalies": true, "Demoted": true, "Undecidable": true, "ByDesign": true,
		"PrunedSkips": true, "GapSkips": true, "NoData": true, "NoDataMemory": true, "RetainedShare": true, "ReadEarly": true,
		"LateSeries": true, "ReadHolds": true, "ReadHeld": true, "OverdueEpisodes": true, "EvaluatingStrategies": true}
	kept := map[string]bool{"AwaitingFirstRound": true, "Recovered": true, "Dependencies": true}
	var full Snapshot
	fill(reflect.ValueOf(&full).Elem(), 0)
	head := reflect.ValueOf(headOf(full))
	for index := 0; index < head.NumField(); index++ {
		field := head.Field(index)
		if kind := field.Kind(); kind != reflect.Slice && kind != reflect.Map {
			continue
		}
		name, value := head.Type().Field(index).Name, field.Len()
		switch {
		case rows[name] == kept[name]:
			t.Errorf("Snapshot.%s is in neither or both lists: decide whether its head carries it", name)
		case rows[name] && value != 0:
			t.Errorf("Snapshot.%s is rows and is still in the head", name)
		case kept[name] && value == 0:
			t.Errorf("Snapshot.%s is kept and is not in the head", name)
		}
	}
}

// A published tally that names a strategy past the end of its own list is
// refused, not read as a smaller set or a crash.
func TestATallyNamingAStrategyItDoesNotListIsRefused(t *testing.T) {
	var tally ImpactTally
	err := json.Unmarshal([]byte(`{"strategies":[["901","2"]],"parts":{"anomalies":{"objects":1,"listed":1,"strategies":[1]}}}`), &tally)
	if err == nil {
		t.Fatalf("read %+v, want a refusal", tally)
	}
	if err := json.Unmarshal([]byte(`{"strategies":[["901","2"]],"parts":{"anomalies":{"objects":1,"listed":1,"strategies":[0]}}}`),
		&tally); err != nil || len(tally.Parts[ImpactAnomalies].Strategies) != 1 {
		t.Fatalf("read %+v error %v, want the one strategy", tally, err)
	}
}

// A summary read under one replica's key that names another is refused, as
// a snapshot that does: read as the key's replica it would put one
// replica's numbers under another's name.
func TestASummaryUnderAnotherReplicasKeyIsRefused(t *testing.T) {
	_, client := realRedis(t)
	store := mustStore(t, client, time.Minute, 0)
	misplaced, err := json.Marshal(SummaryOf(summarizedSnapshot("pod-b", 2), nil, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(context.Background(), store.summaryKey("pod-a"), misplaced, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if summaries, err := store.LoadSummaries(context.Background(), []string{"pod-a"}); err == nil {
		t.Fatalf("read %+v, want a refusal", summaries)
	}
}
