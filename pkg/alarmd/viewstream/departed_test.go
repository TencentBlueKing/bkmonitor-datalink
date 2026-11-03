// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	pb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

func departedWorkers(stats viewstream.Stats) []string {
	workers := []string{}
	for _, departed := range stats.Departed {
		workers = append(workers, departed.WorkerID)
	}
	return workers
}

// A Worker that left -- out of the round's ready set and with no stream --
// is said to have left, not to be lagging, and the version is counted over
// the Workers still there, with no new publication. Out of the ready set
// but still connected it is alive (warming, degraded) and stays lagging: a
// real lag that matters the moment it is ready again.
func TestAReceiverThatLeftIsNamedDepartedNotLagging(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	desired.Ready, desired.ReadyKnown = []string{"w1", "w2"}, true
	if _, err := harness.server.Publish(ctx, desired); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	snap1 := w1.recvSnapshot()
	w1.receipt("i1", snap1.Version, true)
	w2 := harness.connect("w2", "t2", "i2", nil)
	_ = w2.recvSnapshot()
	eventually(t, "w1 installed and w2 sent", func() bool {
		counts := harness.server.Stats().Counts
		return counts.Installed == 1 && counts.Sent == 2
	})

	// w2 drops out of the ready set but keeps its stream: still lagging.
	desired.Ready = []string{"w1"}
	published, err := harness.server.Publish(ctx, desired)
	if err != nil || published.Changed {
		t.Fatalf("a ready-set change alone published %+v (%v), want nothing new", published, err)
	}
	stats := harness.server.Stats()
	if stats.Counts.Expected != 2 || len(stats.Lagging) != 1 || stats.Lagging[0].WorkerID != "w2" || !stats.Lagging[0].Connected || len(stats.Departed) != 0 {
		t.Fatalf("not ready but connected: expected %d lagging %+v departed %v, want w2 lagging and connected", stats.Counts.Expected, stats.Lagging, departedWorkers(stats))
	}

	// Its stream goes too: now it has left, and the version is installed by
	// every Worker still there.
	harness.clock.Add((viewstream.IdleTimeout / 2).Milliseconds())
	w1.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if w1.recv().GetHeartbeat() == nil {
		t.Fatal("heartbeat not answered")
	}
	harness.clock.Add((viewstream.IdleTimeout/2 + time.Second).Milliseconds())
	if reason := w2.recvRefusal(); reason != viewstream.RefusalIdle {
		t.Fatalf("idle worker: %s", reason)
	}
	eventually(t, "w2 departed", func() bool { return len(harness.server.Stats().Departed) == 1 })
	stats = harness.server.Stats()
	if stats.Counts.Expected != 1 || stats.Counts.Installed != 1 || len(stats.Lagging) != 0 || departedWorkers(stats)[0] != "w2" {
		t.Fatalf("after w2 left: counts %+v lagging %+v departed %v", stats.Counts, stats.Lagging, departedWorkers(stats))
	}
	// The moment is reported right after the departure is recorded, on the
	// closing session's goroutine: wait for it, then hold it to once.
	eventually(t, "installed_by_all reported", func() bool { return harness.observer.count("installed_by_all", "") >= 1 })
	if got := harness.observer.count("installed_by_all", ""); got != 1 {
		t.Fatalf("installed_by_all observed %d times, want once when w2 left", got)
	}
}

// Without a ready set -- a publication from outside the round -- nobody is
// taken to have left, however many streams are gone. The round's ready set
// then says it on the publication itself, with nothing new published: a
// Worker outside it with no stream has left, and one inside it with no
// stream is lagging.
func TestTheRoundsReadySetIsWhatSaysWhoLeft(t *testing.T) {
	harness := startServer(t)
	ctx := context.Background()
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	if _, err := harness.server.Publish(ctx, desired); err != nil {
		t.Fatal(err)
	}
	stats := harness.server.Stats()
	if stats.Counts.Expected != 2 || len(stats.Lagging) != 2 || len(stats.Departed) != 0 {
		t.Fatalf("no ready set: expected %d lagging %+v departed %v", stats.Counts.Expected, stats.Lagging, departedWorkers(stats))
	}
	desired.Ready, desired.ReadyKnown = []string{"w1"}, true
	if published, err := harness.server.Publish(ctx, desired); err != nil || published.Changed {
		t.Fatalf("the ready set published %+v (%v), want nothing new", published, err)
	}
	stats = harness.server.Stats()
	if stats.Counts.Expected != 1 || len(stats.Lagging) != 1 || stats.Lagging[0].WorkerID != "w1" || stats.Lagging[0].Connected ||
		len(stats.Departed) != 1 || stats.Departed[0].WorkerID != "w2" {
		t.Fatalf("w1 ready and w2 not, neither connected: expected %d lagging %+v departed %v, want w1 lagging and w2 gone",
			stats.Counts.Expected, stats.Lagging, departedWorkers(stats))
	}
	// A new term starts without a ready set: the last term's says nothing
	// about who has left until this term's round publishes its own.
	if err := harness.server.Lead(8); err != nil {
		t.Fatal(err)
	}
	desired.ControlEpoch, desired.Ready, desired.ReadyKnown = 8, nil, false
	if _, err := harness.server.Publish(ctx, desired); err != nil {
		t.Fatal(err)
	}
	if stats = harness.server.Stats(); len(stats.Departed) != 0 || stats.Counts.Expected != 2 {
		t.Fatalf("a new term's first publication: departed %v expected %d, want nobody gone before its round says so",
			departedWorkers(stats), stats.Counts.Expected)
	}
}

// What the ledger does with Workers that left: one that installed before it
// went stays counted -- it did install -- and one that had not leaves the
// denominator. The moment every remaining Worker holds the version is
// recorded once: a Worker that comes back and catches up later does not
// record it again or move it.
func TestALeftReceiverLeavesTheCountAndTheInstalledMomentStays(t *testing.T) {
	now := time.Unix(100, 0)
	ledger := viewstream.NewLedger(func() time.Time { return now })
	v1 := viewstream.Key{ControlEpoch: 7, Revision: 1}
	ledger.Open(v1, map[string]string{"w1": "d1", "w2": "d2", "w3": "d3"})
	install := func(worker, incarnation, digest string) viewstream.Recorded {
		receiver := viewstream.Receiver{WorkerID: worker, Incarnation: incarnation}
		ledger.MarkSent(v1, receiver)
		return ledger.Record(viewstream.Receipt{Receiver: receiver, Version: viewstream.Version{ControlEpoch: 7, Revision: 1, Digest: digest}, Acked: true, Installed: true})
	}
	install("w1", "i1", "d1")
	install("w3", "i3", "d3")
	now = now.Add(5 * time.Second)
	// w3 installed and then left; w2 never installed and left.
	gone := map[string]bool{"w2": true, "w3": true}
	recorded := ledger.SetDeparted(func(worker string) bool { return gone[worker] })
	if counts, _ := ledger.Counts(v1); counts.Expected != 2 || counts.Installed != 2 {
		t.Fatalf("after w2 and w3 left: %+v, want w3 still counted and w2 out", counts)
	}
	if departed := ledger.Departed(); len(departed) != 1 || departed[0].WorkerID != "w2" {
		t.Fatalf("departed = %+v, want w2 only", departed)
	}
	if !recorded.InstalledByAll || recorded.Elapsed != 5*time.Second || recorded.Expected != 2 {
		t.Fatalf("leaving recorded %+v, want installed by all 2 after 5s", recorded)
	}
	// w2 comes back, as a new process, not yet installed: counted again and
	// lagging, and the version is short of installed again.
	now = now.Add(5 * time.Second)
	ledger.MarkSent(v1, viewstream.Receiver{WorkerID: "w2", Incarnation: "i2b"})
	if again := ledger.SetDeparted(func(string) bool { return false }); again.InstalledByAll {
		t.Fatalf("w2 back recorded %+v", again)
	}
	if counts, _ := ledger.Counts(v1); counts.Expected != 3 || counts.Installed != 2 || len(ledger.Departed()) != 0 {
		t.Fatalf("w2 back: %+v departed %+v", counts, ledger.Departed())
	}
	// It catches up: every receiver holds the version again, and the moment it was
	// first held by every Worker there is not recorded a second time.
	if late := install("w2", "i2b", "d2"); late.InstalledByAll {
		t.Fatalf("w2 catching up recorded %+v, want the first moment kept", late)
	}
	if counts, _ := ledger.Counts(v1); counts.Installed != counts.Expected {
		t.Fatalf("w2 caught up: %+v", counts)
	}
}
