// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

const (
	g3aChildFlag           = "ALARMD_G3A_CRASH_CHILD"
	g3aChildStage          = "ALARMD_G3A_CRASH_STAGE"
	g3aChildRedisAddress   = "ALARMD_G3A_REDIS_ADDRESS"
	g3aChildStatePrefix    = "ALARMD_G3A_STATE_PREFIX"
	g3aChildOwnerPrefix    = "ALARMD_G3A_OWNER_PREFIX"
	g3aChildProgressPrefix = "ALARMD_G3A_PROGRESS_PREFIX"
	g3aChildKafkaBroker    = "ALARMD_G3A_KAFKA_BROKER"
	g3aChildKafkaTopic     = "ALARMD_G3A_KAFKA_TOPIC"
	g3aChildFence          = "ALARMD_G3A_OWNER_FENCE"

	crashBeforeEventACK  = "BEFORE_EVENT_ACK"
	crashAfterEventACK   = "AFTER_EVENT_ACK"
	crashAfterStateApply = "AFTER_STATE_APPLY"
	crashComplete        = "COMPLETE"
	crashExitCode        = 86

	// g3aProduceAPIKey is Kafka's Produce API key and
	// g3aProduceVersionWithHeaders the first Produce version that carries a
	// record batch, and with it record headers. Written here rather than
	// imported from the kafka package so this case states the protocol it
	// requires of its broker in its own words: a test that took the number
	// from the code under test would agree with it however it changed.
	g3aProduceAPIKey             int16 = 0
	g3aProduceVersionWithHeaders int16 = 3

	// g3aAlertIDPrefix marks the line the child prints the alert identity on.
	// The parent cannot read it off the broker -- sarama hands back no
	// records -- so the two runs report theirs and the parent compares them,
	// which is two processes agreeing rather than one vouching for itself.
	g3aAlertIDPrefix = "g3a-alert-id: "
)

type crashWindowCase struct {
	name             string
	stage            string
	wantEventsBefore int
	wantStateBefore  bool
	wantEventsAfter  int
}

// TestG3ACrashWindowsWithRealRedisAndSubprocess kills a real child process at
// each of the three points between a Slot's three durable writes and reads
// what survived.
//
// It runs by default. It used to be gated behind ALARMD_G3A_CRASH_E2E=1
// because it needed a Kafka distribution, a ZooKeeper and a whitespace-free
// JDK on the machine; the gate meant the case had never run outside the one
// session that wrote it, and three separate breakages had accumulated in it
// unnoticed. What these windows prove is alarmd's own write ordering and what
// is visible after each step -- not broker replication -- so the broker is now
// sarama's MockBroker in this process: a real Kafka protocol server, speaking
// the version it negotiates, reached over a real socket by a real child
// process. Redis stays a real redis-server and the crash stays a real
// os.Exit of a real OS process, because those two are what the windows are
// about.
//
// The one thing the in-process broker cannot do is hand back the bytes it was
// given: sarama keeps a ProduceRequest's records unexported and its
// MockResponse interface cannot be implemented outside the package, so the
// ledger here counts produce requests rather than decoding them. What makes
// that count readable as events is pinned at the other end, in
// g3aCrashEventSink: a batch that is not exactly one event fails the child.
func TestG3ACrashWindowsWithRealRedisAndSubprocess(t *testing.T) {
	redisAddress := startG3ARedis(t)

	tests := []crashWindowCase{
		{name: "event ACK before", stage: crashBeforeEventACK, wantEventsBefore: 0, wantEventsAfter: 1},
		{name: "event ACK after state before", stage: crashAfterEventACK, wantEventsBefore: 1, wantEventsAfter: 2},
		{name: "state after progress before", stage: crashAfterStateApply, wantEventsBefore: 1, wantStateBefore: true, wantEventsAfter: 1},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runG3ACrashWindow(t, redisAddress, index, test)
		})
	}
}

// TestG3ACrashWindowChild is a test-binary-only process entry. The parent
// launches it at one exact crash stage; production binaries have no injection seam.
func TestG3ACrashWindowChild(t *testing.T) {
	if os.Getenv(g3aChildFlag) != "1" {
		t.Skip("G3a crash-window child is launched by the parent integration test")
	}
	fence := execution.OwnerFence{}
	if err := json.Unmarshal([]byte(os.Getenv(g3aChildFence)), &fence); err != nil {
		t.Fatalf("decode owner fence: %v", err)
	}
	ownerStore := openG3AOwnershipStore(t, os.Getenv(g3aChildRedisAddress), os.Getenv(g3aChildOwnerPrefix))
	stateStore, stateBackend := openG3AStateStore(t, os.Getenv(g3aChildRedisAddress), os.Getenv(g3aChildStatePrefix))
	t.Cleanup(func() { _ = stateBackend.Close() })
	progressStore := openG3AProgressStore(t, ownerStore, os.Getenv(g3aChildProgressPrefix))
	admitter, err := ownership.NewAdmitter(ownerStore, g3aActivePlanReader{})
	if err != nil {
		t.Fatal(err)
	}
	eventSink, err := enginekafka.OpenTriggerEventSink(enginekafka.DecisionSinkConfig{
		Brokers: []string{os.Getenv(g3aChildKafkaBroker)}, OutputTopic: os.Getenv(g3aChildKafkaTopic),
		ClientID:      "alarmd-g3a-crash-child",
		BrokerVersion: "2.1.0", MaxMessageBytes: 512 << 10,
	})
	if err != nil {
		t.Fatalf("open real TriggerEvent sink: %v", err)
	}
	t.Cleanup(func() { _ = eventSink.Close() })

	trace := make([]string, 0, len(fullTrace))
	fixturePorts := &recordingPorts{trace: &trace, ready: true}
	stage := os.Getenv(g3aChildStage)
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: fixturePorts,
		Finalization: fixturePorts, Activation: fixturePorts,
		Query: fixturePorts, Sequencer: fixturePorts, Evaluator: fixturePorts,
		Admission: admitter, GapGuard: fixturePorts, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness,
		Events:   &g3aCrashEventSink{delegate: eventSink, stage: stage},
		State:    &g3aCrashStateStore{delegate: stateStore, stage: stage},
		Progress: progressStore, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
	})
	if err != nil {
		t.Fatalf("new Coordinator: %v", err)
	}
	request := slotRequest(execution.OperationReplay)
	request.OwnerFence = fence
	result, err := coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("execute after restart result=%+v error=%v trace=%v", result, err, trace)
	}
	if stage != crashComplete {
		t.Fatalf("crash stage %q did not terminate the child", stage)
	}
}

func runG3ACrashWindow(t *testing.T, redisAddress string, index int, test crashWindowCase) {
	t.Helper()
	unique := fmt.Sprintf("%d-%d", time.Now().UnixNano(), index)
	topic := "alarmd-g3a-crash-" + unique
	// One broker per window, so what it recorded is this window's and a count
	// taken before the crash cannot be another case's leftovers.
	broker := startG3AMockBroker(t, topic)
	kafkaBroker := broker.Addr()
	ownerPrefix := "alarmd-g3a-owner-" + unique
	statePrefix := "alarmd-g3a-state-" + unique
	progressPrefix := "alarmd-g3a-progress-" + unique
	ownerStore := openG3AOwnershipStore(t, redisAddress, ownerPrefix)
	stateStore, stateBackend := openG3AStateStore(t, redisAddress, statePrefix)
	t.Cleanup(func() { _ = stateBackend.Close() })
	progressStore := openG3AProgressStore(t, ownerStore, progressPrefix)

	now := time.Now().Truncate(time.Millisecond)
	authority, err := ownerStore.AcquireControlLeader(context.Background(), "control-"+unique, now, 3*time.Minute)
	if err != nil {
		t.Fatalf("acquire control leader: %v", err)
	}
	assignment, err := ownerStore.PublishAssignment(context.Background(), authority, ownership.AssignmentDecision{
		QueryGroup: frozenContract().Slot.QueryGroup, DesiredWorkerID: "worker-old-" + unique,
		ExpectedRecordRevision: 0, PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
	})
	if err != nil {
		t.Fatalf("publish old assignment: %v", err)
	}
	oldLease, err := ownerStore.Acquire(context.Background(), frozenContract().Slot.QueryGroup,
		assignment.DesiredWorkerID, now, g3aCrashedLeaseTTL)
	if err != nil {
		t.Fatalf("acquire old owner: %v", err)
	}

	crashOutput := runG3AChild(t, redisAddress, kafkaBroker, ownerPrefix, statePrefix, progressPrefix,
		topic, test.stage, oldLease.Fence, true)
	beforeEvents := g3aEventsAtBroker(t, broker)
	if beforeEvents != test.wantEventsBefore {
		t.Fatalf("events after crash=%d, want=%d", beforeEvents, test.wantEventsBefore)
	}
	// The child said it sent this many and the broker says it received that
	// many. Two independent counts of one population, which is what keeps a
	// silent refusal from reading as a crash that happened earlier than it
	// did: before this the sink was rejecting every event and the window
	// still reported the number it expected.
	if sent := len(g3aAlertIDsIn(crashOutput)); sent != beforeEvents {
		t.Fatalf("the child reported sending %d events and the broker received %d", sent, beforeEvents)
	}
	beforeState := loadG3AState(t, stateStore)
	if test.wantStateBefore {
		assertG3AAppliedState(t, beforeState)
	} else if beforeState.Status != execution.StateMissingWarming {
		t.Fatalf("state after crash=%+v, want missing", beforeState)
	}
	assertG3ACrashLeftTheSlotUnfinished(t, progressStore)

	newWorkerID := "worker-new-" + unique
	assignment, err = ownerStore.PublishAssignment(context.Background(), authority, ownership.AssignmentDecision{
		QueryGroup: frozenContract().Slot.QueryGroup, DesiredWorkerID: newWorkerID,
		ExpectedRecordRevision: assignment.RecordRevision, PlacementReason: ownership.PlacementRendezvous,
		DecidedAt: oldLease.Deadline,
	})
	if err != nil {
		t.Fatalf("publish takeover assignment: %v", err)
	}
	newLease := acquireG3AAfterLeaseLapse(t, ownerStore, newWorkerID)
	assertG3AStaleProgressCannotFillCrashGap(t, progressStore, oldLease.Fence)
	replayOutput := runG3AChild(t, redisAddress, kafkaBroker, ownerPrefix, statePrefix, progressPrefix,
		topic, crashComplete, newLease.Fence, false)

	afterEvents := g3aEventsAtBroker(t, broker)
	if afterEvents != test.wantEventsAfter {
		t.Fatalf("events after restart=%d, want=%d", afterEvents, test.wantEventsAfter)
	}
	// A replayed Slot must produce the same alert, not a second one: alert_id
	// is what the consumer deduplicates on, so a restart that minted a fresh
	// one would turn every crash into a duplicate alert downstream. The two
	// values come from two different processes.
	identities := append(g3aAlertIDsIn(crashOutput), g3aAlertIDsIn(replayOutput)...)
	if len(identities) != afterEvents {
		t.Fatalf("the two runs reported %d alert identities and the broker received %d events",
			len(identities), afterEvents)
	}
	for position, identity := range identities {
		if identity == "" || identity != identities[0] {
			t.Fatalf("alert identity %d = %q, want the same alert the run before the crash minted (%q)",
				position, identity, identities[0])
		}
	}
	afterState := loadG3AState(t, stateStore)
	assertG3AAppliedState(t, afterState)
	if test.wantStateBefore && afterState.BlobRevision != beforeState.BlobRevision {
		t.Fatalf("state replay revision=%d, want short-circuit at %d", afterState.BlobRevision, beforeState.BlobRevision)
	}
	afterProgress, err := progressStore.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: frozenContract().Slot.QueryGroup})
	if err != nil || afterProgress.Status != execution.ProgressFound || afterProgress.Progress == nil {
		t.Fatalf("Progress after restart=(%+v, %v), want found", afterProgress, err)
	}
	wantNext := frozenContract().Slot.EvaluationTime + 60
	if afterProgress.Progress.NextSlot != wantNext || afterProgress.Progress.LastFullSlot != frozenContract().Slot.EvaluationTime ||
		afterProgress.Progress.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("Progress after restart=%+v", afterProgress.Progress)
	}
	assertG3AStaleProgressReadback(t, ownerStore, progressStore, progressPrefix, oldLease.Fence, wantNext)
}

func runG3AChild(
	t *testing.T,
	redisAddress, kafkaBroker, ownerPrefix, statePrefix, progressPrefix, topic, stage string,
	fence execution.OwnerFence,
	wantCrash bool,
) string {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	encodedFence, err := json.Marshal(fence)
	if err != nil {
		t.Fatalf("encode owner fence: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, testBinary, "-test.run=^TestG3ACrashWindowChild$", "-test.count=1")
	command.Env = append(os.Environ(),
		g3aChildFlag+"=1",
		g3aChildStage+"="+stage,
		g3aChildRedisAddress+"="+redisAddress,
		g3aChildStatePrefix+"="+statePrefix,
		g3aChildOwnerPrefix+"="+ownerPrefix,
		g3aChildProgressPrefix+"="+progressPrefix,
		g3aChildKafkaBroker+"="+kafkaBroker,
		g3aChildKafkaTopic+"="+topic,
		g3aChildFence+"="+string(encodedFence),
	)
	output, runErr := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("child stage %q timed out: %v\n%s", stage, ctx.Err(), output)
	}
	if wantCrash {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != crashExitCode {
			t.Fatalf("child stage %q exit=(%v), want code %d\n%s", stage, runErr, crashExitCode, output)
		}
		return string(output)
	}
	if runErr != nil {
		t.Fatalf("child stage %q failed: %v\n%s", stage, runErr, output)
	}
	return string(output)
}

// g3aAlertIDsIn is every alert identity a child run reported handing the sink.
//
// The identity is read from the child's own output because the broker cannot
// give it back. That is worth exactly what it is: not proof that the bytes
// carried it -- linkdoutput/converter_test.go proves alert_id is the series
// identity -- but proof that two separate processes, one before a crash and
// one after it, minted the same one for the same Slot.
func g3aAlertIDsIn(output string) []string {
	identities := make([]string, 0, 2)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if after, found := strings.CutPrefix(line, g3aAlertIDPrefix); found {
			identities = append(identities, after)
		}
	}
	return identities
}

type g3aCrashEventSink struct {
	delegate execution.EventSink
	stage    string
}

// WriteBatch crashes at the stage under test, and before that gives each event
// the routing a real Plan carries.
//
// The shared worker fixture builds events with no wire format and no snapshot
// reference, which ResolveOutputWireFormat reads as the Python-compatible
// protocol -- a protocol whose branch then refuses the event, because the
// fixture has no frozen compatibility context either. Nothing would reach the
// broker at all, and the counts this whole case is built on would be
// unreachable. Stamping the routing here rather than in the shared fixture
// keeps it out of the dozens of other cases that use the same events and do
// not send them anywhere.
//
// The format is the standard raw event, which is what a Plan carrying a
// snapshot revision resolves to and what the alert pipeline actually
// consumes. The Python-compatible encoding is not re-proved here: its message
// shape is pinned by cases in kafka/trigger_event_sink_test.go that run by
// default, and what these three windows are about is how many messages
// survive a crash, not which encoder produced them.
func (sink *g3aCrashEventSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	// One event per batch is what lets the parent read its produce-request
	// count as an event count: the broker can say how many requests arrived
	// and not how many records each carried, so the second number is pinned
	// here, where it is known. A fixture that grew a second event would
	// otherwise leave the parent quietly counting batches and calling them
	// events.
	if len(events) != 1 {
		fmt.Fprintf(os.Stderr, "g3a crash fixture wrote a batch of %d events; the parent counts produce "+
			"requests and reads them as events, which only holds at one\n", len(events))
		os.Exit(crashExitCode + 1)
	}
	if sink.stage == crashBeforeEventACK {
		os.Exit(crashExitCode)
	}
	routed := make([]contract.TriggerEventV1, len(events))
	for index := range events {
		routed[index] = events[index]
		routed[index].WireFormat = contract.WireFormatStandardRawEvent
		routed[index].StrategyRef = &contract.StrategySnapshotRef{
			TenantID: events[index].TenantID, BusinessID: 2, StrategyID: 1001, Revision: 7,
		}
		// The series identity, which the standard converter turns into
		// alert_id -- the field the alert pipeline deduplicates on. Without
		// it the converter refuses the event outright ("a decision without a
		// series identity has no alert identity"), which is how this case
		// spent its whole life sending nothing while reporting a crash stage
		// that never fired. Derived from the event's own identity so a
		// replayed Slot mints the same alert rather than a second one; the
		// parent compares the two runs' values.
		routed[index].DedupeMD5 = fmt.Sprintf("%x", md5.Sum([]byte(events[index].EventID)))
	}
	fmt.Fprintf(os.Stderr, "%s%s\n", g3aAlertIDPrefix, routed[0].DedupeMD5)
	if err := sink.delegate.WriteBatch(ctx, routed); err != nil {
		// Said out loud because the Coordinator does not re-raise it: a write
		// the sink refuses becomes a Plan outcome, the Slot still completes,
		// and the parent then sees only "the crash stage never fired". That
		// is how this case sat broken -- the sink was refusing every event
		// and the failure it produced named something else entirely.
		fmt.Fprintf(os.Stderr, "g3a crash fixture: the real sink refused the batch: %v\n", err)
		return err
	}
	if sink.stage == crashAfterEventACK {
		os.Exit(crashExitCode)
	}
	return nil
}

type g3aCrashStateStore struct {
	delegate execution.StateStore
	stage    string
}

func (store *g3aCrashStateStore) LoadRuntime(
	ctx context.Context,
	request execution.StatePreflightRequest,
) (execution.StatePreflightResult, error) {
	return store.delegate.LoadRuntime(ctx, request)
}

func (store *g3aCrashStateStore) AdmitRuntime(
	ctx context.Context,
	request execution.StateApplyRequest,
) (execution.StateAdmissionResult, error) {
	return store.delegate.AdmitRuntime(ctx, request)
}

func (store *g3aCrashStateStore) ApplyRuntime(
	ctx context.Context,
	request execution.StateApplyRequest,
) (execution.StateApplyResult, error) {
	result, err := store.delegate.ApplyRuntime(ctx, request)
	if err != nil {
		return result, err
	}
	if store.stage == crashAfterStateApply {
		for _, item := range result.Items {
			if item.Status == execution.StateApplied {
				os.Exit(crashExitCode)
			}
		}
	}
	return result, nil
}

type g3aActivePlanReader struct{}

func (g3aActivePlanReader) IsPlanActive(
	_ context.Context,
	contractRef execution.FrozenExecutionContractRef,
	plan execution.PlanKey,
	epoch execution.StateApplyEpoch,
) (bool, error) {
	return contractRef == frozenContract() && plan.PlanIdentity == planIdentity() && plan.ShardIndex == 0 && epoch == 1, nil
}

type g3aFixedSlotResolver struct{}

func (g3aFixedSlotResolver) NextSlotAfter(
	_ context.Context,
	_ execution.QueryGroupIdentity,
	current execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	return current + 60, nil
}

func openG3AOwnershipStore(t *testing.T, address, prefix string) *ownership.RedisStore {
	t.Helper()
	store, err := ownership.NewRedisStore(ownership.RedisStoreOptions{
		Address: address, DB: 0, Prefix: prefix,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 4,
	})
	if err != nil {
		t.Fatalf("open ownership Redis store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("ping ownership Redis store: %v", err)
	}
	return store
}

func openG3AStateStore(t *testing.T, address, prefix string) (*state.ExecutionStore, *state.RedisBackend) {
	t.Helper()
	backend, err := state.NewRedisBackend(state.RedisBackendOptions{
		Address: address, DB: 0,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 4,
	})
	if err != nil {
		t.Fatalf("open state Redis backend: %v", err)
	}
	if err := backend.Ping(context.Background()); err != nil {
		_ = backend.Close()
		t.Fatalf("ping state Redis backend: %v", err)
	}
	router, err := state.NewFixedRouter("g3a-real-redis", backend)
	if err != nil {
		_ = backend.Close()
		t.Fatalf("create state router: %v", err)
	}
	store, err := state.NewExecutionStore(state.ExecutionStoreOptions{
		Prefix: prefix, Router: router, MaxValueBytes: 1 << 20, MaxItemsPerCall: 16, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		_ = backend.Close()
		t.Fatalf("open execution state store: %v", err)
	}
	return store, backend
}

func openG3AProgressStore(t *testing.T, control *ownership.RedisStore, prefix string) *progress.Store {
	t.Helper()
	store, err := progress.NewStore(progress.StoreOptions{
		Prefix: prefix, Control: control, Slots: g3aFixedSlotResolver{}, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("open Progress store: %v", err)
	}
	return store
}

func loadG3AState(t *testing.T, store *state.ExecutionStore) execution.RuntimeStateView {
	t.Helper()
	input := validInternalExecution()
	result, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{
		Contract: input.Contract,
		Items:    input.StatePreflight,
	})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("load Runtime State=(%+v, %v)", result, err)
	}
	return result.Items[0]
}

func assertG3AAppliedState(t *testing.T, got execution.RuntimeStateView) {
	t.Helper()
	want := validStateMutation()
	if got.Status != execution.StateFoundReady || got.BlobRevision != 1 ||
		got.PersistedApplyVersion != want.ApplyVersion || got.PersistedMutationDigest != want.MutationDigest {
		t.Fatalf("Runtime State=%+v, want persisted ApplyVersion/digest at revision 1", got)
	}
}

// assertG3ACrashLeftTheSlotUnfinished is what "nothing was committed" looks
// like in the Progress record after a crash mid-Slot.
//
// Not an absent record. The Slot begins its Progress before it does anything
// with a side effect, so a crash leaves the attempt written down: the record
// exists, it names this Slot as unfinished, and its cursor has not moved past
// the Slot nor recorded any completion. The case used to require the record
// to be missing, which stopped being true once a Slot began its Progress --
// and because the whole case was behind an environment flag, the assertion
// went on describing a version of the worker that no longer existed. It is
// also the stronger statement: a crash that had committed something would
// satisfy "missing" only by losing the record entirely.
// g3aCrashedLeaseTTL is how long the owner that crashes holds its lease.
//
// Short because the next owner has to wait it out for real. Long enough that
// the child, which takes a fraction of it, never has a fenced write refused
// for a lease that expired underneath it.
//
// It is shorter than the per-batch output admission needs (decision-016 batch
// 2 wants OutputAdmissionMargin + OutputBatchBound, eleven seconds, left on
// the lease before it starts a batch). That costs nothing here because the
// child builds its Coordinator from ports and carries no LeaseAuthority on
// the context, so the sink admits as it always did. Wiring a Session into
// this fixture would make every batch deferred instead of sent, and the
// windows would go quiet rather than red -- so that change has to raise this
// TTL above the admission window with it.
const g3aCrashedLeaseTTL = 2 * time.Second

// acquireG3AAfterLeaseLapse takes the Query Group over the way a survivor
// does after a crash: by waiting out the dead owner's lease.
//
// A crashed owner releases nothing, so the lease stays held until it expires.
// Expiry is judged on Redis's own clock now (decision-016 batch 1b removed
// the caller's clock from the fence), so this can no longer be staged by
// handing the store an instant past the old deadline -- the store would look
// at its own clock and refuse, which it did. Waiting is what is left, and it
// is also what actually happens in production.
func acquireG3AAfterLeaseLapse(t *testing.T, store *ownership.RedisStore, workerID string) ownership.Lease {
	t.Helper()
	deadline := time.Now().Add(g3aCrashedLeaseTTL + 10*time.Second)
	for {
		lease, err := store.Acquire(context.Background(), frozenContract().Slot.QueryGroup,
			workerID, time.Now(), 2*time.Minute)
		if err == nil {
			return lease
		}
		if !errors.Is(err, ownership.ErrLeaseBusy) {
			t.Fatalf("acquire new owner: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the crashed owner's lease was still held %s after its TTL", g3aCrashedLeaseTTL)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func assertG3ACrashLeftTheSlotUnfinished(t *testing.T, store *progress.Store) {
	t.Helper()
	loaded, err := store.LoadProgress(context.Background(),
		execution.ProgressIdentity{QueryGroup: frozenContract().Slot.QueryGroup})
	if err != nil || loaded.Status != execution.ProgressFound || loaded.Progress == nil {
		t.Fatalf("Progress after crash=(%+v, %v), want the begun Slot recorded", loaded, err)
	}
	if loaded.Progress.UnfinishedSlot == nil ||
		loaded.Progress.UnfinishedSlot.Contract.Slot.EvaluationTime != frozenContract().Slot.EvaluationTime {
		t.Fatalf("Progress after crash=%+v, want this Slot named as the unfinished one", *loaded.Progress)
	}
	if loaded.Progress.NextSlot != frozenContract().Slot.EvaluationTime ||
		loaded.Progress.LastFullSlot != 0 || loaded.Progress.LastCompletionKind != "" {
		t.Fatalf("Progress after crash=%+v, want the cursor still on this Slot and no completion recorded",
			*loaded.Progress)
	}
}

func assertG3AStaleProgressCannotFillCrashGap(
	t *testing.T,
	store *progress.Store,
	fence execution.OwnerFence,
) {
	t.Helper()
	result, err := store.CommitProgress(context.Background(), g3aProgressRequest(fence, frozenContract().Slot.EvaluationTime))
	if err != nil || result.Status != execution.ProgressStaleOwner {
		t.Fatalf("stale owner crash-gap commit=(%+v, %v), want STALE_OWNER", result, err)
	}
	// Refused and without a trace: the record is still the one the crash left
	// behind. Requiring it to be missing asked the wrong question once a Slot
	// began its Progress -- and would have been satisfied by a refusal that
	// deleted the record, which is the opposite of leaving it alone.
	assertG3ACrashLeftTheSlotUnfinished(t, store)
}

func assertG3AStaleProgressReadback(
	t *testing.T,
	control *ownership.RedisStore,
	store *progress.Store,
	prefix string,
	fence execution.OwnerFence,
	nextSlot execution.EvaluationTime,
) {
	t.Helper()
	namespace := prefix + ":progress"
	before, missing, err := control.ReadControl(context.Background(), fence.QueryGroup, namespace)
	if err != nil || missing {
		t.Fatalf("read committed Progress before stale replay=(missing=%v, err=%v)", missing, err)
	}
	result, err := store.CommitProgress(context.Background(), g3aProgressRequest(fence, nextSlot))
	if err != nil || result.Status != execution.ProgressStaleOwner {
		t.Fatalf("stale owner Progress replay=(%+v, %v), want STALE_OWNER", result, err)
	}
	after, missing, err := control.ReadControl(context.Background(), fence.QueryGroup, namespace)
	if err != nil || missing || !bytes.Equal(after, before) {
		t.Fatalf("stale owner changed Progress readback: missing=%v err=%v before=%q after=%q", missing, err, before, after)
	}
}

func g3aProgressRequest(fence execution.OwnerFence, slot execution.EvaluationTime) execution.ProgressCommitRequest {
	contractRef := frozenContract()
	contractRef.Slot.EvaluationTime = slot
	return execution.ProgressCommitRequest{
		Identity:         execution.ProgressIdentity{QueryGroup: contractRef.Slot.QueryGroup},
		OwnerFence:       fence,
		ExpectedNextSlot: slot,
		Completion: execution.SlotCompletion{
			Contract: contractRef,
			Kind:     execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessFull,
				DataState:    execution.DataStateData,
			},
			Result: observability.ResultSuccess,
		},
	}
}

// startG3AMockBroker is the Kafka the child writes to: sarama's in-process
// protocol server on a real socket, answering ApiVersions, Metadata and
// Produce for this window's topic.
//
// It answers Produce up to v3, the first version that carries record
// batches and so record headers, because the standard raw event puts the
// tenant in a header and the sink refuses to send it to a cluster that
// takes no headers. A broker that stopped at v2 would therefore make these
// three windows measure the refusal rather than the crash.
func startG3AMockBroker(t *testing.T, topic string) *sarama.MockBroker {
	t.Helper()
	broker := sarama.NewMockBroker(t, 1)
	t.Cleanup(broker.Close)
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetLeader(topic, 0, broker.BrokerID()),
		"ApiVersionsRequest": sarama.NewMockWrapper(&sarama.ApiVersionsResponse{
			ApiVersions: []*sarama.ApiVersionsResponseBlock{
				{ApiKey: g3aProduceAPIKey, MinVersion: 0, MaxVersion: g3aProduceVersionWithHeaders},
			},
		}),
		"ProduceRequest": sarama.NewMockProduceResponse(t).SetVersion(g3aProduceVersionWithHeaders),
	})
	return broker
}

// g3aEventsAtBroker is how many events reached the broker so far.
//
// Counted as produce requests, which is the most the far end can say: sarama
// keeps ProduceRequest.records unexported and its MockResponse interface
// takes unexported types, so no code outside that package can decode what a
// mock broker was handed. The count reads as events because of the two ends
// that are pinned: g3aCrashEventSink refuses a batch that is not exactly one
// event, and the produce version asserted here is the one that carries a
// record batch, so one request here is one event there.
//
// What those bytes look like is proved where they exist, in
// linkdoutput/converter_test.go: the consumer's field names, alert_id, and
// alert_id being the same for two conversions of one event -- which is the
// property the replay windows below depend on and used to re-assert by
// reading the topic.
func g3aEventsAtBroker(t *testing.T, broker *sarama.MockBroker) int {
	t.Helper()
	produced := 0
	for _, exchange := range broker.History() {
		produce, ok := exchange.Request.(*sarama.ProduceRequest)
		if !ok {
			continue
		}
		if produce.Version != g3aProduceVersionWithHeaders {
			t.Fatalf("broker received produce v%d, want v%d: below it the standard raw event's "+
				"tenant header cannot be sent at all", produce.Version, g3aProduceVersionWithHeaders)
		}
		produced++
	}
	return produced
}

func startG3ARedis(t *testing.T) string {
	t.Helper()
	// Skipped rather than failed when redis-server is missing, which is how
	// every other real-Redis case in this tree behaves: the machine either
	// has it and the windows run, or it does not and nothing here can be
	// decided. A failure would only teach people to set a flag again.
	redisServer, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveG3AAddress(t)
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split Redis address %q: %v", address, err)
	}
	process := startG3AProcess(t, redisServer, nil,
		"--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--protected-mode", "no")
	waitG3ATCP(t, address, process, 10*time.Second)
	return address
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type g3aProcess struct {
	command  *exec.Cmd
	output   *lockedBuffer
	stopOnce sync.Once
}

func startG3AProcess(t *testing.T, executable string, environment []string, arguments ...string) *g3aProcess {
	t.Helper()
	command := exec.Command(executable, arguments...)
	if environment != nil {
		command.Env = environment
	}
	output := &lockedBuffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatalf("start %s: %v", executable, err)
	}
	process := &g3aProcess{command: command, output: output}
	t.Cleanup(process.Stop)
	return process
}

func (process *g3aProcess) Stop() {
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	process.stopOnce.Do(func() {
		_ = process.command.Process.Kill()
		_ = process.command.Wait()
	})
}

func waitG3ATCP(t *testing.T, address string, process *g3aProcess, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process did not listen on %s within %s\n%s", address, timeout, process.output.String())
}

func reserveG3AAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TCP address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved TCP address: %v", err)
	}
	return address
}

var _ execution.EventSink = (*g3aCrashEventSink)(nil)
var _ execution.StateStore = (*g3aCrashStateStore)(nil)

// freshFrozenRenewals is what a state double that is not about renewals
// answers: one item per requested series, nothing renewed. The caller
// validates that a store answered for exactly the series it was asked about,
// so a double that answered for a different set would fail every Slot.
func freshFrozenRenewals(request execution.FrozenStateRenewalRequest) execution.FrozenStateRenewalResult {
	result := execution.FrozenStateRenewalResult{Items: make([]execution.FrozenStateRenewalItem, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.FrozenStateRenewalItem{
			Identity: item.Identity, Outcome: execution.FrozenRenewalFresh,
		}
	}
	return result
}

func (store *g3aCrashStateStore) RenewFrozenRuntime(
	_ context.Context, request execution.FrozenStateRenewalRequest,
) (execution.FrozenStateRenewalResult, error) {
	return freshFrozenRenewals(request), nil
}
