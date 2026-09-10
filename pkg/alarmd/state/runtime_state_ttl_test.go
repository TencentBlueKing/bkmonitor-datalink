// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ttlRecordingBackend records the TTL of every Runtime State write, on both the
// sequential and the pipelined path.
type ttlRecordingBackend struct {
	*pipelineMemoryBackend
	ttls []time.Duration
}

func newTTLRecordingBackend() *ttlRecordingBackend {
	return &ttlRecordingBackend{pipelineMemoryBackend: newPipelineMemoryBackend()}
}

func (backend *ttlRecordingBackend) CompareAndSet(
	ctx context.Context, key string, expected []byte, missing bool, value []byte, ttl time.Duration,
) (bool, error) {
	backend.ttls = append(backend.ttls, ttl)
	return backend.pipelineMemoryBackend.CompareAndSet(ctx, key, expected, missing, value, ttl)
}

func (backend *ttlRecordingBackend) CompareAndSetManyByDigest(
	ctx context.Context, guard *FenceGuard, writes []FencedWrite,
) ([]FencedWriteOutcome, error) {
	for _, write := range writes {
		backend.ttls = append(backend.ttls, write.TTL)
	}
	return backend.pipelineMemoryBackend.CompareAndSetManyByDigest(ctx, guard, writes)
}

func ttlTestRetention(points uint32, interval, lateness time.Duration) []execution.StateRetentionRequirement {
	return []execution.StateRetentionRequirement{
		{LevelID: 1, RetentionPoints: points, EvaluationInterval: interval, LatenessTolerance: lateness},
	}
}

// TestRuntimeStateWriteTTLFollowsPlanRetention pins the TTL every Runtime State
// write is stored with to what the Plan actually keeps. Writing at the
// configured ceiling instead - a flat 30 days in production, refreshed every
// round - is what left more than half of the stored keys alive long after
// anything could read them.
func TestRuntimeStateWriteTTLFollowsPlanRetention(t *testing.T) {
	// Five one-minute points plus the store's one-minute restart margin.
	retention := ttlTestRetention(5, time.Minute, 0)
	const wantTTL = 6 * time.Minute
	version := applyVersion()

	t.Run("sequential path", func(t *testing.T) {
		backend := newTTLRecordingBackend()
		store := newBatchStore(t, backend, nil)
		mutations := seriesMutations(t, 3, version, 0)
		// No preflight, so every item takes the read-then-compare-and-set path.
		result, err := store.ApplyRuntime(context.Background(),
			execution.StateApplyRequest{Contract: frozenRef(), Retention: retention, Items: mutations})
		if err != nil {
			t.Fatalf("ApplyRuntime() error = %v", err)
		}
		for _, item := range result.Items {
			if item.Status != execution.StateApplied {
				t.Fatalf("item %+v was not applied", item)
			}
		}
		assertRecordedTTLs(t, backend.ttls, len(mutations), wantTTL)
	})

	t.Run("pipelined path", func(t *testing.T) {
		backend := newTTLRecordingBackend()
		store := newBatchStore(t, backend, fixedFenceKeys{keys: testFenceKeys()})
		mutations := seriesMutations(t, 3, version, 0)
		if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{
			Contract: frozenRef(), Items: preflightItems(mutations),
		}); err != nil {
			t.Fatalf("LoadRuntime() error = %v", err)
		}
		result, err := store.ApplyRuntimeFenced(context.Background(),
			execution.StateApplyRequest{Contract: frozenRef(), Retention: retention, Items: mutations}, testApplyFence())
		if err != nil {
			t.Fatalf("ApplyRuntimeFenced() error = %v", err)
		}
		for _, item := range result.Items {
			if item.Status != execution.StateApplied {
				t.Fatalf("item %+v was not applied", item)
			}
		}
		if backend.pipelines == 0 {
			t.Fatal("the pipelined path was not taken")
		}
		assertRecordedTTLs(t, backend.ttls, len(mutations), wantTTL)
	})
}

func assertRecordedTTLs(t *testing.T, recorded []time.Duration, want int, wantTTL time.Duration) {
	t.Helper()
	if len(recorded) != want {
		t.Fatalf("recorded %d write TTLs, want %d", len(recorded), want)
	}
	for index, ttl := range recorded {
		if ttl != wantTTL {
			t.Fatalf("write %d stored with TTL %s, want the retention-derived %s", index, ttl, wantTTL)
		}
	}
}

// TestDerivedStateTTLOutlivesTheWindowHorizon is the safety condition the whole
// change rests on. retentionCutoff keeps points back to
// (RetentionPoints-1)*interval + lateness; StateTTL stores the key for
// RetentionPoints*interval + lateness + restart margin. Read from one
// requirement the difference is exactly one interval plus the margin, so a key
// can only expire after its window would already have pruned every point it
// held. Two separately built requirements could disagree on any of the three
// fields and invert this, which is why NewLevelRequirement is the only place
// they are composed.
func TestDerivedStateTTLOutlivesTheWindowHorizon(t *testing.T) {
	const (
		minTTL        = time.Minute
		maxTTL        = 30 * 24 * time.Hour
		restartMargin = 10 * time.Minute
	)
	fingerprint := strings.Repeat("a", 64)
	for _, points := range []uint32{1, 2, 5, 60, 1440} {
		for _, interval := range []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute} {
			for _, lateness := range []time.Duration{0, 2 * interval, time.Hour} {
				name := fmt.Sprintf("points=%d/interval=%s/lateness=%s", points, interval, lateness)
				t.Run(name, func(t *testing.T) {
					requirement := NewLevelRequirement(
						execution.StateRetentionRequirement{LevelID: 1, RetentionPoints: points,
							EvaluationInterval: interval, LatenessTolerance: lateness},
						fingerprint, points,
					)
					ttl, err := StateTTL([]LevelRequirement{requirement}, restartMargin, minTTL, maxTTL)
					if err != nil {
						t.Fatalf("StateTTL() error = %v", err)
					}
					// A latest source time far past every horizon, so the cutoff
					// is not clamped at the start of the epoch.
					const latest = int64(4_000_000_000)
					horizon := time.Duration(latest-retentionCutoff(latest, requirement)) * time.Second
					if ttl <= horizon {
						t.Fatalf("TTL %s does not outlive the retained horizon %s", ttl, horizon)
					}
					// Below MinTTL the clamp makes the TTL longer still, which
					// keeps the ordering but not the exact difference.
					if want := horizon + interval + restartMargin; ttl != want && ttl != minTTL {
						t.Fatalf("TTL %s, want horizon plus one interval and the restart margin (%s)", ttl, want)
					}
				})
			}
		}
	}
}

// TestRuntimeWindowIsInertOnceItsDerivedTTLCouldExpire answers what shortening
// the TTL costs a series that stops producing data. A key only reaches its
// derived TTL after going unwritten for that whole span, and by then the points
// it still holds are older than any window the evaluator can read: the next
// point that ever arrives prunes all of them. The window that survives the
// silence is indistinguishable from one that never had history, so nothing a
// judgement reads differs between letting the key expire and keeping it.
func TestRuntimeWindowIsInertOnceItsDerivedTTLCouldExpire(t *testing.T) {
	const (
		points        = 5
		interval      = time.Minute
		restartMargin = 10 * time.Minute
	)
	fingerprint := strings.Repeat("a", 64)
	requirement := NewLevelRequirement(
		execution.StateRetentionRequirement{LevelID: 1, RetentionPoints: points, EvaluationInterval: interval},
		fingerprint, points,
	)
	ttl, err := StateTTL([]LevelRequirement{requirement}, restartMargin, time.Minute, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("StateTTL() error = %v", err)
	}

	aged, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	const lastLive = int64(1_700_000_000)
	history := make([]StatePoint, points)
	for index := range history {
		history[index] = StatePoint{
			RecordID:   fmt.Sprintf("%064d", index),
			SourceTime: lastLive - int64(points-1-index)*int64(interval/time.Second),
			Levels:     []PointLevelFact{{LevelID: 1, DetectFingerprint: fingerprint, Result: LevelFactAnomalous}},
		}
	}
	if _, err = aged.Apply(history); err != nil {
		t.Fatalf("Apply(history) error = %v", err)
	}
	if view, ok := aged.History(1); !ok || view.Summarize(lastLive, points).Completeness != HistoryFull {
		t.Fatal("the fixture did not build a full window before the silence")
	}

	// The first point after a silence exactly as long as the derived TTL: the
	// earliest moment at which the key could have been gone instead.
	resumed := StatePoint{
		RecordID:   fmt.Sprintf("%064d", 999),
		SourceTime: lastLive + int64(ttl/time.Second),
		Levels:     []PointLevelFact{{LevelID: 1, DetectFingerprint: fingerprint, Result: LevelFactNormal}},
	}
	if _, err = aged.Apply([]StatePoint{resumed}); err != nil {
		t.Fatalf("Apply(resumed) error = %v", err)
	}
	fresh, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err = fresh.Apply([]StatePoint{resumed}); err != nil {
		t.Fatalf("Apply(resumed) on a fresh window error = %v", err)
	}

	if len(aged.points) != 1 || aged.points[0].sourceTime != resumed.SourceTime {
		t.Fatalf("the aged window kept %d points, want only the resuming one", len(aged.points))
	}
	agedView, agedOK := aged.History(1)
	freshView, freshOK := fresh.History(1)
	if !agedOK || !freshOK {
		t.Fatal("Level history is missing")
	}
	if agedView.Summarize(resumed.SourceTime, points) != freshView.Summarize(resumed.SourceTime, points) {
		t.Fatalf("summaries differ: aged=%+v fresh=%+v",
			agedView.Summarize(resumed.SourceTime, points), freshView.Summarize(resumed.SourceTime, points))
	}
	// The whole retained span, which is wider than any trigger or recovery
	// window a Level can ask for, since RetentionPoints covers both.
	from := resumed.SourceTime - int64(points)*int64(interval/time.Second)
	if agedView.CountAnomalies(from, resumed.SourceTime) != freshView.CountAnomalies(from, resumed.SourceTime) {
		t.Fatal("the aged window still contributes anomalies a fresh one does not")
	}
}

// TestRuntimeApplyRefusesRetentionNoTTLCanSatisfy keeps a Plan whose retention
// exceeds the configured ceiling from failing its batch with an opaque error.
// It is refused per item with the deterministic budget reason, at admission -
// before any event is written - and again at apply.
func TestRuntimeApplyRefusesRetentionNoTTLCanSatisfy(t *testing.T) {
	backend := newTTLRecordingBackend()
	store := newBatchStore(t, backend, nil)
	mutations := seriesMutations(t, 2, applyVersion(), 0)
	// newBatchStore caps TTLs at one hour; two hours of retention cannot fit.
	request := execution.StateApplyRequest{Contract: frozenRef(),
		Retention: ttlTestRetention(120, time.Minute, 0), Items: mutations}

	admission, err := store.AdmitRuntime(context.Background(), request)
	if err != nil {
		t.Fatalf("AdmitRuntime() error = %v", err)
	}
	for index, item := range admission.Items {
		if item.Status != execution.StateAdmissionDeterministicInvalid ||
			item.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) {
			t.Fatalf("admission item %d = %+v, want a deterministic budget rejection", index, item)
		}
		if item.Identity != mutations[index].Identity {
			t.Fatalf("admission item %d lost its identity", index)
		}
	}

	applied, err := store.ApplyRuntime(context.Background(), request)
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	for index, item := range applied.Items {
		if item.Status != execution.StateApplyDeterministicInvalid ||
			item.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) {
			t.Fatalf("apply item %d = %+v, want a deterministic budget rejection", index, item)
		}
	}
	if len(backend.ttls) != 0 || len(backend.values) != 0 {
		t.Fatal("a refused Plan still reached storage")
	}
}

// TestRuntimeApplyRequiresPlanRetention keeps the ceiling from becoming a
// silent fallback: a caller that cannot state what its keys must outlive is
// rejected outright rather than served 30 days.
func TestRuntimeApplyRequiresPlanRetention(t *testing.T) {
	backend := newTTLRecordingBackend()
	store := newBatchStore(t, backend, nil)
	request := execution.StateApplyRequest{Contract: frozenRef(), Items: seriesMutations(t, 1, applyVersion(), 0)}

	if _, err := store.AdmitRuntime(context.Background(), request); err == nil {
		t.Fatal("AdmitRuntime() admitted a request without Plan retention")
	}
	if _, err := store.ApplyRuntime(context.Background(), request); err == nil {
		t.Fatal("ApplyRuntime() applied a request without Plan retention")
	}
	if len(backend.values) != 0 {
		t.Fatal("a request without Plan retention reached storage")
	}

	incomplete := request
	incomplete.Retention = ttlTestRetention(0, time.Minute, 0)
	_, err := store.ApplyRuntime(context.Background(), incomplete)
	if err == nil || errors.Is(err, ErrStateBudget) {
		t.Fatalf("ApplyRuntime() with an unusable retention error = %v, want a request error", err)
	}
}
