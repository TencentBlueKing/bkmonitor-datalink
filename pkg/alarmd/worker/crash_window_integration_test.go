// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	g3aAcceptanceFlag      = "ALARMD_G3A_CRASH_E2E"
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
)

type crashWindowCase struct {
	name             string
	stage            string
	wantEventsBefore int
	wantStateBefore  bool
	wantEventsAfter  int
}

func TestG3ACrashWindowsWithRealRedisKafkaSubprocess(t *testing.T) {
	if os.Getenv(g3aAcceptanceFlag) != "1" {
		t.Skip("set ALARMD_G3A_CRASH_E2E=1 to run the real Redis/Kafka process-level acceptance test")
	}
	redisAddress := startG3ARedis(t)
	kafkaBroker := startG3AKafka(t)

	tests := []crashWindowCase{
		{name: "event ACK before", stage: crashBeforeEventACK, wantEventsBefore: 0, wantEventsAfter: 1},
		{name: "event ACK after state before", stage: crashAfterEventACK, wantEventsBefore: 1, wantEventsAfter: 2},
		{name: "state after progress before", stage: crashAfterStateApply, wantEventsBefore: 1, wantStateBefore: true, wantEventsAfter: 1},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runG3ACrashWindow(t, redisAddress, kafkaBroker, index, test)
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
	admitter, err := ownership.NewAdmitter(ownerStore, g3aActivePlanReader{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	eventSink, err := enginekafka.OpenTriggerEventSink(enginekafka.DecisionSinkConfig{
		Brokers: []string{os.Getenv(g3aChildKafkaBroker)}, OutputTopic: os.Getenv(g3aChildKafkaTopic),
		AllowedOutputTopics: []string{os.Getenv(g3aChildKafkaTopic)}, ClientID: "alarmd-g3a-crash-child",
		BrokerVersion: "2.1.0", MaxMessageBytes: 512 << 10,
	})
	if err != nil {
		t.Fatalf("open real TriggerEvent sink: %v", err)
	}
	t.Cleanup(func() { _ = eventSink.Close() })

	trace := make([]string, 0, len(fullTrace))
	fixturePorts := &recordingPorts{trace: &trace, ready: true}
	stage := os.Getenv(g3aChildStage)
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: fixturePorts, Activation: fixturePorts,
		Query: fixturePorts, Sequencer: fixturePorts, Evaluator: fixturePorts,
		Admission: admitter, GapGuard: fixturePorts,
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

func runG3ACrashWindow(t *testing.T, redisAddress, kafkaBroker string, index int, test crashWindowCase) {
	t.Helper()
	unique := fmt.Sprintf("%d-%d", time.Now().UnixNano(), index)
	topic := "alarmd-g3a-crash-" + unique
	createG3AKafkaTopic(t, kafkaBroker, topic)
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
		assignment.DesiredWorkerID, now, 30*time.Second)
	if err != nil {
		t.Fatalf("acquire old owner: %v", err)
	}

	runG3AChild(t, redisAddress, kafkaBroker, ownerPrefix, statePrefix, progressPrefix, topic, test.stage, oldLease.Fence, true)
	beforeEvents := readG3AKafkaEvents(t, kafkaBroker, topic)
	if len(beforeEvents) != test.wantEventsBefore {
		t.Fatalf("events after crash=%d, want=%d", len(beforeEvents), test.wantEventsBefore)
	}
	beforeState := loadG3AState(t, stateStore)
	if test.wantStateBefore {
		assertG3AAppliedState(t, beforeState)
	} else if beforeState.Status != execution.StateMissingWarming {
		t.Fatalf("state after crash=%+v, want missing", beforeState)
	}
	beforeProgress, err := progressStore.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: frozenContract().Slot.QueryGroup})
	if err != nil || beforeProgress.Status != execution.ProgressMissing {
		t.Fatalf("Progress after crash=(%+v, %v), want missing", beforeProgress, err)
	}

	newWorkerID := "worker-new-" + unique
	assignment, err = ownerStore.PublishAssignment(context.Background(), authority, ownership.AssignmentDecision{
		QueryGroup: frozenContract().Slot.QueryGroup, DesiredWorkerID: newWorkerID,
		ExpectedRecordRevision: assignment.RecordRevision, PlacementReason: ownership.PlacementRendezvous,
		DecidedAt: oldLease.Deadline,
	})
	if err != nil {
		t.Fatalf("publish takeover assignment: %v", err)
	}
	newLease, err := ownerStore.Acquire(context.Background(), frozenContract().Slot.QueryGroup,
		newWorkerID, oldLease.Deadline.Add(time.Millisecond), 2*time.Minute)
	if err != nil {
		t.Fatalf("acquire new owner: %v", err)
	}
	assertG3AStaleProgressCannotFillCrashGap(t, progressStore, oldLease.Fence)
	runG3AChild(t, redisAddress, kafkaBroker, ownerPrefix, statePrefix, progressPrefix, topic, crashComplete, newLease.Fence, false)

	afterEvents := readG3AKafkaEvents(t, kafkaBroker, topic)
	if len(afterEvents) != test.wantEventsAfter {
		t.Fatalf("events after restart=%d, want=%d", len(afterEvents), test.wantEventsAfter)
	}
	wantEvent := validTriggerEvent()
	for eventIndex, event := range afterEvents {
		if event.EventID != wantEvent.EventID || event.EventSemanticDigest != wantEvent.EventSemanticDigest {
			t.Fatalf("event[%d] identity=(%q,%q), want stable (%q,%q)", eventIndex,
				event.EventID, event.EventSemanticDigest, wantEvent.EventID, wantEvent.EventSemanticDigest)
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
) {
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
		return
	}
	if runErr != nil {
		t.Fatalf("child stage %q failed: %v\n%s", stage, runErr, output)
	}
}

type g3aCrashEventSink struct {
	delegate execution.EventSink
	stage    string
}

func (sink *g3aCrashEventSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	if sink.stage == crashBeforeEventACK {
		os.Exit(crashExitCode)
	}
	if err := sink.delegate.WriteBatch(ctx, events); err != nil {
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
	plan execution.PlanIdentity,
	epoch execution.StateApplyEpoch,
) (bool, error) {
	return contractRef == frozenContract() && plan == planIdentity() && epoch == 1, nil
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
	loaded, err := store.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: fence.QueryGroup})
	if err != nil || loaded.Status != execution.ProgressMissing {
		t.Fatalf("Progress after stale crash-gap commit=(%+v, %v), want missing", loaded, err)
	}
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

func createG3AKafkaTopic(t *testing.T, broker, topic string) {
	t.Helper()
	config := sarama.NewConfig()
	config.Version = sarama.V2_1_0_0
	config.Net.DialTimeout = 2 * time.Second
	admin, err := sarama.NewClusterAdmin([]string{broker}, config)
	if err != nil {
		t.Fatalf("open Kafka admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); err != nil {
		t.Fatalf("create Kafka topic %q: %v", topic, err)
	}
	t.Cleanup(func() { _ = admin.DeleteTopic(topic) })
}

func readG3AKafkaEvents(t *testing.T, broker, topic string) []contract.TriggerEventV1 {
	t.Helper()
	config := sarama.NewConfig()
	config.Version = sarama.V2_1_0_0
	config.Consumer.Return.Errors = true
	config.Net.DialTimeout = 2 * time.Second
	client, err := sarama.NewClient([]string{broker}, config)
	if err != nil {
		t.Fatalf("open Kafka metadata client: %v", err)
	}
	oldest, err := client.GetOffset(topic, 0, sarama.OffsetOldest)
	if err != nil {
		_ = client.Close()
		t.Fatalf("read Kafka oldest offset: %v", err)
	}
	newest, err := client.GetOffset(topic, 0, sarama.OffsetNewest)
	if err != nil {
		_ = client.Close()
		t.Fatalf("read Kafka newest offset: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close Kafka metadata client: %v", err)
	}
	if newest == oldest {
		return nil
	}

	consumer, err := sarama.NewConsumer([]string{broker}, config)
	if err != nil {
		t.Fatalf("open Kafka consumer: %v", err)
	}
	defer consumer.Close()
	partition, err := consumer.ConsumePartition(topic, 0, oldest)
	if err != nil {
		t.Fatalf("consume Kafka topic: %v", err)
	}
	defer partition.Close()
	events := make([]contract.TriggerEventV1, 0, int(newest-oldest))
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for int64(len(events)) < newest-oldest {
		select {
		case message := <-partition.Messages():
			if message == nil {
				t.Fatal("Kafka partition message channel closed")
			}
			event, decodeErr := contract.DecodeTriggerEventV1(message.Value)
			if decodeErr != nil {
				t.Fatalf("decode TriggerEventV1 at offset %d: %v", message.Offset, decodeErr)
			}
			events = append(events, *event)
		case consumeErr := <-partition.Errors():
			t.Fatalf("consume Kafka topic: %v", consumeErr)
		case <-timer.C:
			t.Fatalf("timed out reading %d Kafka events; got %d", newest-oldest, len(events))
		}
	}
	return events
}

func startG3ARedis(t *testing.T) string {
	t.Helper()
	redisServer, err := exec.LookPath("redis-server")
	if err != nil {
		t.Fatalf("real redis-server is required for G3a crash-window acceptance: %v", err)
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

func startG3AKafka(t *testing.T) string {
	t.Helper()
	kafkaScript, zookeeperScript := resolveG3AKafkaScripts(t)
	javaHome := resolveG3AJavaHome(t)
	zookeeperAddress := reserveG3AAddress(t)
	_, zookeeperPort, err := net.SplitHostPort(zookeeperAddress)
	if err != nil {
		t.Fatalf("split ZooKeeper address %q: %v", zookeeperAddress, err)
	}
	kafkaAddress := reserveG3AAddress(t)
	root := t.TempDir()
	zookeeperConfig := filepath.Join(root, "zookeeper.properties")
	zookeeperData := filepath.Join(root, "zookeeper-data")
	if err := os.MkdirAll(zookeeperData, 0o755); err != nil {
		t.Fatalf("create ZooKeeper data directory: %v", err)
	}
	zookeeperProperties := strings.Join([]string{
		"dataDir=" + zookeeperData,
		"clientPort=" + zookeeperPort,
		"maxClientCnxns=0",
		"admin.enableServer=false",
		"tickTime=2000",
		"initLimit=5",
		"syncLimit=2",
	}, "\n") + "\n"
	if err := os.WriteFile(zookeeperConfig, []byte(zookeeperProperties), 0o600); err != nil {
		t.Fatalf("write ZooKeeper config: %v", err)
	}
	zookeeperLog := filepath.Join(root, "zookeeper-log")
	if err := os.MkdirAll(zookeeperLog, 0o755); err != nil {
		t.Fatalf("create ZooKeeper log directory: %v", err)
	}
	zookeeper := startG3AProcess(t, zookeeperScript, g3AKafkaEnvironment(javaHome, zookeeperLog), zookeeperConfig)
	waitG3ATCP(t, zookeeperAddress, zookeeper, 15*time.Second)

	kafkaConfig := filepath.Join(root, "server.properties")
	kafkaData := filepath.Join(root, "kafka-data")
	if err := os.MkdirAll(kafkaData, 0o755); err != nil {
		t.Fatalf("create Kafka data directory: %v", err)
	}
	kafkaProperties := strings.Join([]string{
		"broker.id=1",
		"listeners=PLAINTEXT://" + kafkaAddress,
		"advertised.listeners=PLAINTEXT://" + kafkaAddress,
		"log.dirs=" + kafkaData,
		"zookeeper.connect=" + zookeeperAddress,
		"num.partitions=1",
		"default.replication.factor=1",
		"offsets.topic.replication.factor=1",
		"transaction.state.log.replication.factor=1",
		"transaction.state.log.min.isr=1",
		"min.insync.replicas=1",
		"auto.create.topics.enable=true",
		"delete.topic.enable=true",
		"group.initial.rebalance.delay.ms=0",
	}, "\n") + "\n"
	if err := os.WriteFile(kafkaConfig, []byte(kafkaProperties), 0o600); err != nil {
		t.Fatalf("write Kafka config: %v", err)
	}
	kafkaLog := filepath.Join(root, "kafka-log")
	if err := os.MkdirAll(kafkaLog, 0o755); err != nil {
		t.Fatalf("create Kafka log directory: %v", err)
	}
	kafka := startG3AProcess(t, kafkaScript, g3AKafkaEnvironment(javaHome, kafkaLog), kafkaConfig)
	waitG3AKafka(t, kafkaAddress, kafka, 25*time.Second)
	return kafkaAddress
}

func resolveG3AKafkaScripts(t *testing.T) (string, string) {
	t.Helper()
	candidates := make([]string, 0, 2)
	if configured := os.Getenv("ALARMD_TEST_KAFKA_HOME"); configured != "" {
		candidates = append(candidates, configured)
	}
	if wrapper, err := exec.LookPath("kafka-server-start"); err == nil {
		resolved, resolveErr := filepath.EvalSymlinks(wrapper)
		if resolveErr == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(resolved)), "libexec"))
		}
	}
	for _, root := range candidates {
		kafkaScript := filepath.Join(root, "bin", "kafka-server-start.sh")
		zookeeperScript := filepath.Join(root, "bin", "zookeeper-server-start.sh")
		if executableFile(kafkaScript) && executableFile(zookeeperScript) {
			return kafkaScript, zookeeperScript
		}
	}
	t.Fatalf("real Kafka and ZooKeeper libexec scripts are required; checked %v", candidates)
	return "", ""
}

func resolveG3AJavaHome(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("ALARMD_TEST_JAVA_HOME"), os.Getenv("JAVA_HOME")}
	installed, _ := filepath.Glob("/Library/Java/JavaVirtualMachines/*/Contents/Home")
	sort.Sort(sort.Reverse(sort.StringSlice(installed)))
	candidates = append(candidates, installed...)
	for _, candidate := range candidates {
		if candidate == "" || strings.ContainsAny(candidate, " \t\r\n") {
			continue
		}
		java := filepath.Join(candidate, "bin", "java")
		if !executableFile(java) {
			continue
		}
		command := exec.Command(java, "-version")
		if err := command.Run(); err == nil {
			return candidate
		}
	}
	t.Fatalf("a whitespace-free working JDK is required for the real Kafka test; checked %v", candidates)
	return ""
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func g3AKafkaEnvironment(javaHome, logDirectory string) []string {
	environment := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "JAVA_HOME=") || strings.HasPrefix(entry, "KAFKA_HEAP_OPTS=") ||
			strings.HasPrefix(entry, "KAFKA_JMX_OPTS=") || strings.HasPrefix(entry, "KAFKA_GC_LOG_OPTS=") ||
			strings.HasPrefix(entry, "LOG_DIR=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"JAVA_HOME="+javaHome,
		"KAFKA_HEAP_OPTS=-Xms256m -Xmx256m",
		"KAFKA_JMX_OPTS=-Dkafka.g3a.test=true",
		"KAFKA_GC_LOG_OPTS=-Dkafka.g3a.gc.test=true",
		"LOG_DIR="+logDirectory,
	)
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

func waitG3AKafka(t *testing.T, broker string, process *g3aProcess, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		config := sarama.NewConfig()
		config.Version = sarama.V2_1_0_0
		config.Net.DialTimeout = 500 * time.Millisecond
		config.Net.ReadTimeout = time.Second
		config.Net.WriteTimeout = time.Second
		client, err := sarama.NewClient([]string{broker}, config)
		if err == nil {
			_ = client.Close()
			return
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Kafka did not become ready on %s within %s: %v\n%s", broker, timeout, lastErr, process.output.String())
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
