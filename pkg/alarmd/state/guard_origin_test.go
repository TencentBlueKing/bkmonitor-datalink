// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func originGapIdentity() execution.PlanGapIdentity {
	return execution.PlanGapIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "301"},
		StateGeneration: "generation"}
}

type originGapStore struct {
	t       *testing.T
	store   *ExecutionStore
	backend *casMemoryBackend
	version execution.ApplyVersion
}

func newOriginGapStore(t *testing.T) *originGapStore {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("test", backend)
	store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096,
		MaxItemsPerCall: 4, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return &originGapStore{t: t, store: store, backend: backend, version: applyVersion()}
}

func (s *originGapStore) load() execution.GapGuardSnapshot {
	s.t.Helper()
	result, err := s.store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(),
		Items: []execution.PlanGapLoadItem{{Identity: originGapIdentity(), ApplyVersion: s.version, ScheduleRevision: "plan-r1"}}})
	if err != nil {
		s.t.Fatal(err)
	}
	return result.Items[0]
}

// apply writes one Level scope mutation in the Slot at s.version.
func (s *originGapStore) apply(kind execution.GapMutationKind, reason string, required uint32) {
	s.t.Helper()
	revision := uint64(0)
	if current := s.load(); current.Status == execution.GapFound {
		revision = current.MarkerRevision
	}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: originGapIdentity(),
		ExpectedMarkerRevision: revision, ApplyVersion: s.version, ScheduleRevision: "plan-r1",
		Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{HasLevel: true, LevelID: 1}, Kind: kind,
			ReasonCode: execution.ReasonCode(reason), RequiredFullSlots: required}}})
	if err != nil {
		s.t.Fatal(err)
	}
	result, err := s.store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{
		Retention: execution.GenerationRetention{Unknown: true}, Contract: frozenRef(), Items: []execution.PlanGapMutation{mutation}})
	if err != nil || result.Items[0].Status != execution.GapGuardApplied {
		s.t.Fatalf("%s: result=%+v err=%v", kind, result, err)
	}
}

func (s *originGapStore) origin() execution.ReasonOrigin {
	s.t.Helper()
	marker := s.load()
	if marker.Status != execution.GapFound || len(marker.Scopes) != 1 {
		s.t.Fatalf("marker %+v, want one scope", marker)
	}
	return marker.Scopes[0].ReasonOrigin()
}

// A gap scope's origin is the Slot whose write opened it: written then, and
// kept through the warmup rounds and a later strengthening under the same
// reason. Re-opened under another reason, it is a new guard with a new origin.
func TestAGapScopeKeepsTheOriginOfTheSlotThatOpenedIt(t *testing.T) {
	s := newOriginGapStore(t)
	opened := s.version.EvaluationTime
	s.apply(execution.GapOpen, contract.ReasonQueryTimeout, 3)
	want := execution.ReasonOrigin{Slot: opened, Cause: contract.ReasonQueryTimeout}
	if got := s.origin(); got != want {
		t.Fatalf("opened: origin %+v, want %+v", got, want)
	}
	s.version.EvaluationTime += 60
	s.apply(execution.GapWarmup, contract.ReasonQueryTimeout, 3)
	if got := s.origin(); got != want {
		t.Fatalf("warming: origin %+v, want the opening Slot's %+v", got, want)
	}
	s.version.EvaluationTime += 60
	s.apply(execution.GapStrengthen, contract.ReasonQueryTimeout, 4)
	if got := s.origin(); got != want {
		t.Fatalf("strengthened under the same reason: origin %+v, want the opening Slot's %+v", got, want)
	}
	s.version.EvaluationTime += 60
	s.apply(execution.GapOpen, contract.ReasonGapSkipped, 4)
	reopened := execution.ReasonOrigin{Slot: s.version.EvaluationTime, Cause: contract.ReasonGapSkipped}
	if got := s.origin(); got != reopened {
		t.Fatalf("re-opened under another reason: origin %+v, want %+v", got, reopened)
	}
}

// A marker written before origins existed loads, keeps warming, and is
// strengthened under its own reason without gaining an origin: a guard that
// was carried is not restamped.
func TestAGapMarkerWrittenWithoutOriginsLoadsAndIsNotGivenOne(t *testing.T) {
	s := newOriginGapStore(t)
	s.apply(execution.GapOpen, contract.ReasonQueryTimeout, 3)
	key, err := PlanGapKeyV2("alarmd", originGapIdentity())
	if err != nil {
		t.Fatal(err)
	}
	// The same marker as an older build wrote it: no origin keys at all.
	var envelope map[string]any
	if err := json.Unmarshal(s.backend.values[key], &envelope); err != nil {
		t.Fatal(err)
	}
	for _, entry := range envelope["scopes"].([]any) {
		scope := entry.(map[string]any)
		delete(scope, "ReasonOriginSlot")
		delete(scope, "ReasonOriginCause")
	}
	older, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(older, []byte("ReasonOrigin")) {
		t.Fatalf("fixture still names an origin: %s", older)
	}
	s.backend.values[key] = older
	if got := s.origin(); got != (execution.ReasonOrigin{}) {
		t.Fatalf("older marker loaded with origin %+v", got)
	}
	s.version.EvaluationTime += 60
	s.apply(execution.GapWarmup, contract.ReasonQueryTimeout, 3)
	s.version.EvaluationTime += 60
	s.apply(execution.GapStrengthen, contract.ReasonQueryTimeout, 4)
	if got := s.origin(); got != (execution.ReasonOrigin{}) {
		t.Fatalf("carried guard given origin %+v", got)
	}
}

func originStateMutation(t *testing.T, origin execution.ReasonOrigin) execution.StateMutation {
	t.Helper()
	identity := execution.StateKeyIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "301"},
		StateGeneration:      execution.StateGeneration(strings.Repeat("c1", 32)),
		SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("ab", 32))}
	mutation := packedMutation(t, []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal, execution.LevelFactUnavailable),
	}, 2)
	mutation.Identity = identity
	mutation.Levels[1].HistoryCompleteness = execution.HistoryGapped
	mutation.Levels[1].GapReasonCode = contract.ReasonQueryTimeout
	mutation.Levels[1] = mutation.Levels[1].WithReasonOrigin(origin)
	return mutation
}

// A Level's origin is written into the framed record and read back; a record
// without one is byte-for-byte the record this build wrote before the field
// existed, and reads back with none.
func TestALevelOriginRoundTripsAndARecordWithoutOneIsUnchanged(t *testing.T) {
	origin := execution.ReasonOrigin{Slot: 1758399940, Cause: contract.ReasonQueryTimeout}
	with := originStateMutation(t, origin)
	raw, err := encodeRuntimePacked(with, 4)
	if err != nil {
		t.Fatal(err)
	}
	view, err := decodeRuntimePacked(raw, with.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Levels[1].ReasonOrigin(); got != origin {
		t.Fatalf("decoded origin %+v, want %+v", got, origin)
	}
	if got := view.Levels[0].ReasonOrigin(); got != (execution.ReasonOrigin{}) {
		t.Fatalf("a FULL Level decoded with origin %+v", got)
	}
	without := originStateMutation(t, execution.ReasonOrigin{})
	plain, err := encodeRuntimePacked(without, 4)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("GapReasonOrigin")) {
		t.Fatalf("a record without an origin names one: %s", plain)
	}
	view, err = decodeRuntimePacked(plain, without.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Levels[1].ReasonOrigin(); got != (execution.ReasonOrigin{}) {
		t.Fatalf("a record without an origin decoded with %+v", got)
	}
}

// A Level's last real point is written into the framed record and read back;
// a record without one names no such key, so a Plan not written to end on no
// data keeps the bytes it had, and reads back with zero - not known.
func TestALastRealPointRoundTripsAndARecordWithoutOneIsUnchanged(t *testing.T) {
	with := originStateMutation(t, execution.ReasonOrigin{})
	with.Levels[0].LastRealPointTime = 1758399880
	raw, err := encodeRuntimePacked(with, 4)
	if err != nil {
		t.Fatal(err)
	}
	view, err := decodeRuntimePacked(raw, with.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if view.Levels[0].LastRealPointTime != 1758399880 || view.Levels[1].LastRealPointTime != 0 {
		t.Fatalf("decoded last real points %d and %d, want 1758399880 and none", view.Levels[0].LastRealPointTime, view.Levels[1].LastRealPointTime)
	}
	without := originStateMutation(t, execution.ReasonOrigin{})
	plain, err := encodeRuntimePacked(without, 4)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("LastRealPointTime")) {
		t.Fatalf("a record without a last real point names one: %s", plain)
	}
}

// The shapes the previous release (d03b7a7b4, deployed as 0.4.4794) decodes
// these records into, copied from that tree: its RuntimeLevelStateMutation,
// GapScopeState, and the framed header and gap envelope that hold them. It
// decodes both with encoding/json's Unmarshal and no DisallowUnknownFields
// (decodeRuntimePacked, decodeGap there), so a key it does not know is
// skipped. Copied rather than imported so a later change to this tree's
// types cannot make the check pass by changing what it checks against.
type previousReleaseLevelState struct {
	LevelID                 uint32
	LevelStateCompatibility string
	HistoryCompleteness     execution.HistoryCompleteness
	GapReasonCode           execution.ReasonCode
	WarmupRequirementRef    string
	LastProcessedEventTime  int64
}

type previousReleasePackedHeader struct {
	Schema         string                     `json:"schema"`
	Identity       execution.StateKeyIdentity `json:"identity"`
	BlobRevision   uint64                     `json:"blob_revision"`
	ApplyVersion   execution.ApplyVersion     `json:"apply_version"`
	MutationDigest execution.MutationDigest   `json:"mutation_digest"`
	LastEventTime  int64                      `json:"last_event_time"`
	SeriesGuard    *execution.StateGuardFact  `json:"series_guard,omitempty"`
	Levels         []struct {
		Mutation          previousReleaseLevelState `json:"mutation"`
		DetectFingerprint string                    `json:"detect_fingerprint"`
	} `json:"levels"`
	PointCount int `json:"point_count"`
}

type previousReleaseGapScopeState struct {
	Scope             execution.GapScope
	Status            execution.GapStatus
	ReasonCode        execution.ReasonCode
	RequiredFullSlots uint32
	ObservedFullSlots uint32
}

type previousReleaseGapEnvelope struct {
	Schema           string                         `json:"schema"`
	Identity         execution.PlanGapIdentity      `json:"identity"`
	MarkerRevision   uint64                         `json:"marker_revision"`
	ApplyVersion     execution.ApplyVersion         `json:"apply_version"`
	MutationDigest   execution.MutationDigest       `json:"mutation_digest"`
	ScheduleRevision execution.PlanScheduleRevision `json:"schedule_revision"`
	Scopes           []previousReleaseGapScopeState `json:"scopes,omitempty"`
}

// The previous release reads a record carrying origins as it reads one
// without: the same header and scope values, the origin keys skipped. A
// rollback onto records this build wrote loads them; what it writes back
// drops the origins, and this build then reports those guards carried with
// no origin rather than inventing one.
func TestThePreviousReleaseDecodesRecordsCarryingOrigins(t *testing.T) {
	origin := execution.ReasonOrigin{Slot: 1758399940, Cause: contract.ReasonQueryTimeout}
	raw, err := encodeRuntimePacked(originStateMutation(t, origin), 4)
	if err != nil {
		t.Fatal(err)
	}
	headerLen, rest, err := consumeUvarint(raw[packedFrameHeaderLen:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rest[:headerLen], []byte("GapReasonOriginSlot")) {
		t.Fatalf("fixture header carries no origin: %s", rest[:headerLen])
	}
	var previous previousReleasePackedHeader
	if err := json.Unmarshal(rest[:headerLen], &previous); err != nil {
		t.Fatalf("previous release refuses the header: %v", err)
	}
	if len(previous.Levels) != 2 || previous.Levels[1].Mutation.GapReasonCode != contract.ReasonQueryTimeout ||
		previous.Levels[1].Mutation.HistoryCompleteness != execution.HistoryGapped || previous.Levels[1].Mutation.LevelID != 2 {
		t.Fatalf("previous release read the Levels as %+v", previous.Levels)
	}

	s := newOriginGapStore(t)
	s.apply(execution.GapOpen, contract.ReasonQueryTimeout, 3)
	key, err := PlanGapKeyV2("alarmd", originGapIdentity())
	if err != nil {
		t.Fatal(err)
	}
	stored := s.backend.values[key]
	if !bytes.Contains(stored, []byte("ReasonOriginSlot")) {
		t.Fatalf("fixture marker carries no origin: %s", stored)
	}
	var marker previousReleaseGapEnvelope
	if err := json.Unmarshal(stored, &marker); err != nil {
		t.Fatalf("previous release refuses the marker: %v", err)
	}
	if marker.Schema != executionGapSchemaV2 || len(marker.Scopes) != 1 ||
		marker.Scopes[0].ReasonCode != contract.ReasonQueryTimeout || marker.Scopes[0].Status != execution.GapStatusGapped ||
		marker.Scopes[0].RequiredFullSlots != 3 {
		t.Fatalf("previous release read the marker as %+v", marker)
	}
}
