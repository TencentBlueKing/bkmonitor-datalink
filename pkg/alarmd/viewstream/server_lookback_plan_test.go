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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// recvPlans is the next message, which must be a lookback plan set.
func (ws *workerStream) recvPlans() *pb.LookbackPlans {
	ws.t.Helper()
	message := ws.recv()
	if message.GetLookbackPlans() == nil {
		ws.t.Fatalf("want a lookback plan set, got %s", describe(message))
	}
	return message.GetLookbackPlans()
}

// The Leader sends each Worker its whole lookback plan set beside its view
// and never in it: the view's revision does not move, a set equal to the
// last one sent is not sent again, a changed one is, and a Worker the set
// names nothing for gets the empty set, which withdraws what it held.
func TestTheLeaderSendsEachWorkerItsPlanSetBesideTheViewNotInIt(t *testing.T) {
	harness := startServer(t)
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	if _, err := harness.server.Publish(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	w2 := harness.connect("w2", "t2", "i2", nil)
	w1.recvSnapshot()
	w2.recvSnapshot()
	expires := time.Unix(5000, 0)
	plans := map[string][]viewstream.LookbackPlan{"w1": {{QueryGroup: "qg-1", Class: viewstream.LookbackPlanSeriesLate, Rung: 3, ExpiresAt: expires}}}
	if err := harness.server.SetLookbackPlans(plans); err != nil {
		t.Fatal(err)
	}
	got := w1.recvPlans()
	if got.ControlEpoch != 7 || len(got.Entries) != 1 || got.Entries[0].QueryGroup != "qg-1" || got.Entries[0].Rung != 3 ||
		got.Entries[0].ExpiresAtMs != expires.UnixMilli() {
		t.Fatalf("w1's plan set = %+v, want qg-1 at rung 3 under epoch 7", got)
	}
	if empty := w2.recvPlans(); len(empty.Entries) != 0 {
		t.Fatalf("w2's plan set = %+v, want the empty set", empty)
	}
	// A set is counted after its Send returns, so the Worker can hold it a
	// moment before the count does.
	eventually(t, "both sets were sent", func() bool { return harness.server.Stats().LookbackPlanSetsSent == 2 })
	if stats := harness.server.Stats(); stats.Revision != 1 || stats.LookbackPlanSetsSent != 2 {
		t.Fatalf("stats = revision %d, %d plan sets sent; want revision 1 unchanged and two sets", stats.Revision, stats.LookbackPlanSetsSent)
	}
	// The same sets again send nothing; a changed one is sent to its Worker.
	if err := harness.server.SetLookbackPlans(plans); err != nil {
		t.Fatal(err)
	}
	plans["w1"][0].Rung = 4
	if err := harness.server.SetLookbackPlans(plans); err != nil {
		t.Fatal(err)
	}
	if changed := w1.recvPlans(); len(changed.Entries) != 1 || changed.Entries[0].Rung != 4 {
		t.Fatalf("w1's changed set = %+v, want rung 4", changed)
	}
	eventually(t, "only the changed set was sent", func() bool { return harness.server.Stats().LookbackPlanSetsSent == 3 })
	if stats := harness.server.Stats(); stats.LookbackPlanSetsSent != 3 || stats.Revision != 1 {
		t.Fatalf("stats = %+v, want three sets and revision 1", stats)
	}
}

// A Leader without plans - a fallback Leader, or a new term before its first
// planning round - sends no plan set at all, so each Worker keeps what it
// holds until it expires. The next term's plans reach a Worker on the stream
// it opens to the new Leader, whole.
func TestAFallbackSendsNoPlansAndTheNextTermsReachANewStreamWhole(t *testing.T) {
	harness := startServer(t)
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	if _, err := harness.server.Publish(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	if err := harness.server.SetLookbackPlans(nil); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	w1.recvSnapshot()
	w1.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if next := w1.recv(); next.GetHeartbeat() == nil {
		t.Fatalf("after the snapshot w1 got %s, want only the heartbeat's answer: no plan set", describe(next))
	}
	if sent := harness.server.Stats().LookbackPlanSetsSent; sent != 0 {
		t.Fatalf("plan sets sent without plans = %d, want 0", sent)
	}

	// Handed back: the term ends, a new one starts and plans; a new stream
	// gets the whole set after its snapshot.
	harness.server.StepDown()
	if err := harness.server.Lead(8); err != nil {
		t.Fatal(err)
	}
	desired.ControlEpoch = 8
	if _, err := harness.server.Publish(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	if err := harness.server.SetLookbackPlans(map[string][]viewstream.LookbackPlan{"w1": {{QueryGroup: "qg-1",
		Class: viewstream.LookbackPlanSeriesLate, Rung: 2, ExpiresAt: time.Unix(5000, 0)}}}); err != nil {
		t.Fatal(err)
	}
	again := harness.connect("w1", "t1", "i2", nil)
	again.recvSnapshot()
	if got := again.recvPlans(); got.ControlEpoch != 8 || len(got.Entries) != 1 {
		t.Fatalf("the new stream's plan set = %+v, want the new term's whole set", got)
	}
}

// A new term starts with no plans, whatever the last one sent: a Worker that
// opens a stream to it gets its view and nothing more until this term plans.
func TestANewTermStartsWithNoPlans(t *testing.T) {
	harness := startServer(t)
	if err := harness.server.Lead(7); err != nil {
		t.Fatal(err)
	}
	desired := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	if err := harness.server.SetLookbackPlans(map[string][]viewstream.LookbackPlan{"w1": {{QueryGroup: "qg-1",
		Class: viewstream.LookbackPlanSeriesLate, Rung: 2, ExpiresAt: time.Unix(5000, 0)}}}); err != nil {
		t.Fatal(err)
	}
	if err := harness.server.Lead(8); err != nil {
		t.Fatal(err)
	}
	desired.ControlEpoch = 8
	if _, err := harness.server.Publish(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	w1 := harness.connect("w1", "t1", "i1", nil)
	w1.recvSnapshot()
	w1.send(&pb.WorkerMessage{Body: &pb.WorkerMessage_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if next := w1.recv(); next.GetHeartbeat() == nil {
		t.Fatalf("a new term's stream got %s after its snapshot, want no plan set before the term plans", describe(next))
	}
}
