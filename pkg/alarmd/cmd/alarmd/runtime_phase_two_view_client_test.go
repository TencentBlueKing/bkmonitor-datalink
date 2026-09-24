// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production Worker finds its own Leader from the records, connects
// over the listener's port, installs the view and executes from it
// (decision-016 batch 4b). With the stream up Slots complete FULL. When the
// stream is taken away the installed view remains and, with nothing changed
// under it, the next Slot completes FULL from it: the view is what the
// Worker runs from, not the connection. When the records then move on
// without it -- a cutover of its own Query Group, brought by the lease
// renewal -- the round is refused by name and the cursor stays. Given back,
// the Worker reconnects, installs the new view by snapshot and completes
// the Slot on the new Segment.
func TestTheProductionWorkerExecutesFromTheViewAndRefusesWhenItGoesStale(t *testing.T) {
	// The registration advertises this listener; the settling runner serves
	// the bundle's control stream on it from the first round.
	address := reserveAddressForBundle(t)
	fixture := startCutoverFixture(t, func(cfg *config.Config) { cfg.HTTP.Listen = address })
	ctx := context.Background()
	bundle := fixture.bundle
	client := bundle.dependencies.ViewClient
	if client == nil {
		t.Fatal("the production bundle has no view client")
	}
	// The Worker found itself as Leader there and installed its own
	// projection: both Query Groups.
	waitFor(t, "the Worker installs the view from its own Leader", func() bool {
		view, ok := client.Installed()
		return ok && view.Version.Revision >= 1 && len(view.Entries) == 2
	})
	stats := client.Stats()
	if !stats.Connected || stats.Leader.WorkerID != fixture.cfg.PhaseTwo.Worker.ID || !strings.HasSuffix(stats.Leader.Endpoint, address[strings.LastIndex(address, ":"):]) || stats.ObjectsMissing != 0 {
		t.Fatalf("client stats = %+v, want connected to this replica at the listener with no object missing", stats)
	}
	waitFor(t, "the Leader counts the install", func() bool {
		leader := bundle.dependencies.ViewStreamStats()
		return leader.Counts.Installed == 1 && leader.Sessions == 1
	})

	// Execution with the stream up: the next Slot completes FULL.
	withStream := runOneSlotFull(t, fixture)

	// The stream is taken away: the Worker is disconnected and keeps
	// trying. The view it installed stays, nothing under it changed, and
	// the next Slot completes FULL from it, the cursor moving the same.
	withholdControlStreamForTest(bundle)
	waitFor(t, "the Worker notices the stream is gone", func() bool { return !client.Stats().Connected })
	installed, held := client.Installed()
	if !held {
		t.Fatal("the installed view went with the connection")
	}
	withoutStream := runOneSlotFull(t, fixture)
	if withoutStream.CompletionKind != withStream.CompletionKind || withoutStream.Result != withStream.Result || withoutStream.ReasonCode != withStream.ReasonCode {
		t.Fatalf("Slot without the stream = %+v, with it %+v: the connection, not the view, decided execution", withoutStream, withStream)
	}
	if progress := fixture.progress(ctx); progress.NextSlot <= withStream.slot {
		t.Fatalf("the cursor did not advance past the Slot run with the stream: %+v", progress)
	}

	// The records move on without the Worker: its own Query Group is cut
	// over, which stamps a new timeline revision on the assignment; the
	// lease renewal brings it. The view still says the old one, so the
	// round is refused by name and the cursor stays.
	installCutoverStallStrategies(t, ctx, fixture.redisClient, "system.disk", 1725000600)
	stripSegmentContent(t, ctx, fixture.redisClient, productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog"), fixture.queryGroup)
	for round := 0; round < 2; round++ {
		if err := bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("cutover refresh %d error = %v", round, err)
		}
	}
	record, err := fixture.production.dependencies.Store.ReadAssignment(ctx, fixture.queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	staleEntry, inView := client.Entry(fixture.queryGroup)
	if !inView || record.TimelineRecordRevision == 0 || staleEntry.Assignment.TimelineRecordRevision == record.TimelineRecordRevision {
		t.Fatalf("assignment revision %d, view revision %d in view %t: the cutover did not move the records past the installed view %d", record.TimelineRecordRevision, staleEntry.Assignment.TimelineRecordRevision, inView, installed.Version.Revision)
	}
	if err := fixture.session.Renew(ctx, fixture.now(), fixture.cfg.PhaseTwo.Ownership.LeaseTTL.Duration()); err != nil {
		t.Fatal(err)
	}
	before := fixture.progress(ctx)
	unsettled := fixture.runner.(*settlingRuntime).phaseTwoQueryGroupRuntime
	stale := runOneAttempt(t, fixture, unsettled)
	if stale.Completed || stale.Result != observability.ResultRetrying || stale.ReasonCode != execution.ReasonCode(contract.ReasonViewNotExecutable) {
		t.Fatalf("Slot on a stale view = %+v, want a retrying %s", stale, contract.ReasonViewNotExecutable)
	}
	if progress := fixture.progress(ctx); progress.NextSlot != before.NextSlot || progress.LastFullSlot != before.LastFullSlot {
		t.Fatalf("the cursor moved on a stale view: %+v, was %+v", progress, before)
	}
	if counts := fixture.production.viewGate.Counts(); counts["timeline_stale"] != 1 {
		t.Fatalf("gate outcomes after the stale round = %v, want this Query Group counted timeline_stale", counts)
	}
	// The refusal reached the log by name with the gate's word, and the
	// Runner's outcome the fleet reads says the same.
	var dueLines, runnerLines int
	for _, observation := range fixture.observed() {
		switch {
		case observation.Stage == observability.StageScheduleDue && observation.ReasonCode == observability.ReasonCode(contract.ReasonViewNotExecutable):
			if observation.Result != observability.ResultRetrying || observation.Err == nil || !strings.Contains(observation.Err.Error(), "timeline_stale") {
				t.Fatalf("schedule_due refusal line = %+v, want retrying with the gate's word timeline_stale", observation)
			}
			dueLines++
		case observation.Stage == observability.StageRunnerReturned && observation.RunOutcome == "view_not_executable":
			runnerLines++
		}
	}
	if dueLines != 1 || runnerLines != 1 {
		t.Fatalf("refusal lines: schedule_due %d, runner_returned{view_not_executable} %d, want one each", dueLines, runnerLines)
	}

	// Given back: the Worker reconnects, installs the new view by snapshot
	// and the Slot completes on the new Segment.
	restoreControlStreamForTest(bundle)
	waitFor(t, "the Worker reconnects and installs the new view", func() bool {
		stats := client.Stats()
		entry, inView := client.Entry(fixture.queryGroup)
		return stats.Connected && stats.Connections >= 2 && inView && entry.Assignment.TimelineRecordRevision == record.TimelineRecordRevision
	})
	restored := runOneSlotFull(t, fixture)
	if restored.CompletionKind != withStream.CompletionKind {
		t.Fatalf("Slot after the stream returned = %+v, want %+v", restored, withStream)
	}
	if counts := fixture.production.viewGate.Counts(); counts["timeline_stale"] != 0 || counts["executable"] == 0 {
		t.Fatalf("gate outcomes after the stream returned = %v, want the Query Group executable again", counts)
	}
}

type slotOutcome struct {
	slot           execution.EvaluationTime
	CompletionKind execution.CompletionKind
	Result         observability.Result
	ReasonCode     execution.ReasonCode
}

// runOneSlotFull advances the fixture's clock to the next grid point and
// runs the Runner until one Slot completes FULL, returning what it said.
func runOneSlotFull(t *testing.T, fixture *cutoverStallFixture) slotOutcome {
	t.Helper()
	ctx := context.Background()
	for attempt := 0; attempt < 200; attempt++ {
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		at := fixture.now()
		result, attempted, err := fixture.runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("RunOne: %v", err)
		}
		if !attempted {
			next := at.Unix() - at.Unix()%60 + 60
			fixture.clock.Store(next*1000 + 1500)
			continue
		}
		if result.Completed && result.CompletionKind == execution.CompletionFull {
			return slotOutcome{slot: fixture.progress(ctx).LastFullSlot, CompletionKind: result.CompletionKind, Result: result.Result, ReasonCode: result.ReasonCode}
		}
		fixture.clock.Store(at.Add(time.Second).UnixMilli())
	}
	t.Fatal("no FULL Slot within 200 attempts")
	return slotOutcome{}
}

// runOneAttempt advances the fixture's clock until the runner attempts a
// round and returns what that one round said, whatever it was.
func runOneAttempt(t *testing.T, fixture *cutoverStallFixture, runner phaseTwoQueryGroupRuntime) execution.SlotExecutionResult {
	t.Helper()
	ctx := context.Background()
	for attempt := 0; attempt < 200; attempt++ {
		if nextAt := runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		at := fixture.now()
		result, attempted, err := runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("RunOne: %v", err)
		}
		if attempted {
			return result
		}
		next := at.Unix() - at.Unix()%60 + 60
		fixture.clock.Store(next*1000 + 1500)
	}
	t.Fatal("no attempted round within 200 tries")
	return execution.SlotExecutionResult{}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within ten seconds", what)
}

// trackedListener remembers every connection it accepted so a test can
// close them all, hijacked ones included.
type trackedListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func listenTracked(address string) (*trackedListener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &trackedListener{Listener: listener}, nil
}

func (listener *trackedListener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	listener.mu.Lock()
	listener.conns = append(listener.conns, conn)
	listener.mu.Unlock()
	return conn, nil
}

func (listener *trackedListener) closeAll() {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	for _, conn := range listener.conns {
		_ = conn.Close()
	}
	listener.conns = nil
}

func reserveAddressForBundle(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

// The execution path does not import the view stream: the hard condition
// four of decision-016's shadow step, pinned on the import graph rather
// than on one behaviour. A package on the Slot's path that starts to
// depend on viewstream fails here before it can read a view.
func TestTheHotPathDoesNotImportTheViewStream(t *testing.T) {
	const module = "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/"
	for _, pkg := range []string{"scheduler", "worker", "controlplane", "execution", "state", "progress", "kafka", "ownership", "evaluation", "access"} {
		out, err := exec.Command("go", "list", "-deps", module+pkg).Output()
		if err != nil {
			t.Fatalf("go list %s: %v", pkg, err)
		}
		for _, dep := range strings.Split(string(out), "\n") {
			if strings.HasSuffix(dep, "/pkg/alarmd/viewstream") || strings.HasSuffix(dep, "/pkg/alarmd/viewstream/pb") {
				t.Fatalf("%s depends on %s: the hot path must not read the view before the cutover decision", pkg, dep)
			}
		}
	}
}
