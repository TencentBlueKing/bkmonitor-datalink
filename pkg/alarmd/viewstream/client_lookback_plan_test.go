// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// planClient starts a Worker's client against leader with a plan handler
// that keeps every set it is handed; nil handler when keep is false.
func planClient(t *testing.T, leader *scriptedLeader, keep bool) (*viewstream.Client, func() [][]viewstream.LookbackPlan) {
	t.Helper()
	dialer := &bufconnDialer{}
	dialer.serveOn(t, "leader", leader)
	discovery := &scriptedDiscovery{}
	discovery.set("leader", true)
	var mu sync.Mutex
	var sets [][]viewstream.LookbackPlan
	options := viewstream.ClientOptions{Dial: dialer.dial, Tick: 20 * time.Millisecond,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond): // fixed-wait: the stub Sleep's real pause in place of the client's backoff, which the case sets so the loop cannot spin
				return nil
			}
		}}
	if keep {
		options.LookbackPlans = func(plans []viewstream.LookbackPlan) {
			mu.Lock()
			sets = append(sets, plans)
			mu.Unlock()
		}
	}
	client, err := viewstream.NewClient(viewstream.ClientIdentity{WorkerID: "w1", Incarnation: "i1", StreamToken: "t1"},
		discovery, &countingProbe{missing: map[string]struct{}{}}, &sessionObserver{}, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return client, func() [][]viewstream.LookbackPlan {
		mu.Lock()
		defer mu.Unlock()
		return append([][]viewstream.LookbackPlan(nil), sets...)
	}
}

// planLeader answers a Hello with the snapshot of v1, then the messages in
// after, and every heartbeat with one, so the stream is never cut as silent.
func planLeader(after ...*pb.LeaderMessage) (*scriptedLeader, viewstream.View) {
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	v1 := viewFrom(desired, "w1", 1)
	leader := &scriptedLeader{}
	for _, chunk := range viewstream.SnapshotChunks(v1, 0) {
		leader.onHello = append(leader.onHello, &pb.LeaderMessage{Body: &pb.LeaderMessage_Snapshot{Snapshot: chunk}})
	}
	leader.onHello = append(leader.onHello, after...)
	leader.replies = func(message *pb.WorkerMessage) []*pb.LeaderMessage {
		if message.GetHeartbeat() == nil {
			return nil
		}
		return []*pb.LeaderMessage{{Body: &pb.LeaderMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}}}
	}
	return leader, v1
}

// A plan set reaches the Worker whole and changes nothing of its view: the
// version, its digest and the receipts stay what the snapshot made them, so
// tier-2 advice never moves the view the Slots execute under. Entries no
// Worker could act on - no Query Group, a class this build does not know -
// are not handed on.
func TestALookbackPlanSetReachesTheWorkerAndMintsNoViewVersion(t *testing.T) {
	expires := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	plans := viewstream.LookbackPlansMessage(7, []viewstream.LookbackPlan{
		{QueryGroup: "qg-1", Class: viewstream.LookbackPlanSeriesLate, Rung: 2, Priority: 1, ExpiresAt: expires},
		{QueryGroup: "qg-2", Class: "read_early", Rung: 1, ExpiresAt: expires},
		{QueryGroup: "", Class: viewstream.LookbackPlanSeriesLate, Rung: 1, ExpiresAt: expires},
	})
	leader, v1 := planLeader(plans)
	client, sets := planClient(t, leader, true)
	eventually(t, "the plan set is handed on", func() bool { return len(sets()) == 1 })
	got := sets()[0]
	if len(got) != 1 || got[0].QueryGroup != "qg-1" || got[0].Rung != 2 || got[0].Priority != 1 || !got[0].ExpiresAt.Equal(expires) {
		t.Fatalf("the plan set handed on = %+v, want qg-1's entry alone", got)
	}
	eventually(t, "heartbeats run after the plans", func() bool { return leader.got("heartbeat") >= 3 })
	view, ok := client.Installed()
	if !ok || view.Version != v1.Version {
		t.Fatalf("installed view %+v (%t), want the snapshot's version %+v unchanged", view.Version, ok, v1.Version)
	}
	if receipts := leader.got("receipt_installed"); receipts != 1 {
		t.Fatalf("receipts = %d, want the snapshot's one: a plan set is not a view", receipts)
	}
	if stats := client.Stats(); stats.LookbackPlanSets != 1 || stats.Connections != 1 {
		t.Fatalf("stats = %+v, want one plan set on one connection", stats)
	}
}

// A Worker that does not know a Leader message ignores it, with no error and
// no reconnect: a field this build has no name for decodes to an empty body,
// as the plan set does on a build from before plans. The same holds for a
// Worker that knows plans but has nothing to hand them to.
func TestAWorkerIgnoresALeaderMessageItDoesNotKnow(t *testing.T) {
	unknown := &pb.LeaderMessage{}
	raw := protowire.AppendTag(nil, 7, protowire.BytesType)
	raw = protowire.AppendBytes(raw, []byte("a message from a later build"))
	unknown.ProtoReflect().SetUnknown(raw)
	plans := viewstream.LookbackPlansMessage(7, []viewstream.LookbackPlan{
		{QueryGroup: "qg-1", Class: viewstream.LookbackPlanSeriesLate, Rung: 2, ExpiresAt: time.Now().Add(time.Hour)}})
	leader, v1 := planLeader(unknown, plans)
	client, _ := planClient(t, leader, false)
	eventually(t, "the plan set arrives", func() bool { return client.Stats().LookbackPlanSets == 1 })
	eventually(t, "heartbeats run after both", func() bool { return leader.got("heartbeat") >= 3 })
	if stats := client.Stats(); stats.Connections != 1 || len(stats.Refusals) != 0 {
		t.Fatalf("stats = %+v, want one connection and no refusal", stats)
	}
	if view, ok := client.Installed(); !ok || view.Version != v1.Version {
		t.Fatalf("installed view %+v (%t), want the snapshot's", view.Version, ok)
	}
}
