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
	// Five one-minute points plus the store's one-minute restart margin, moved
	// half a step past the whole minute so the expiry never lands at the phase
	// the Slot writes at.
	retention := ttlTestRetention(5, time.Minute, 0)
	const wantTTL = 6*time.Minute + 30*time.Second
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
					// keeps the ordering but not the exact difference. Above
					// it the TTL is the horizon plus one interval and the
					// margin, moved forward by under one interval to sit half a
					// step off the write phase.
					base := horizon + interval + restartMargin
					if ttl != minTTL && (ttl < base || ttl >= base+interval) {
						t.Fatalf("TTL %s, want horizon plus one interval and the restart margin (%s) moved forward by under one interval", ttl, base)
					}
					if ttl != minTTL && ttl%interval != interval/2 {
						t.Fatalf("TTL %s sits %s into a %s step, want half a step: an expiry at the write phase is what a returning series meets between its read and its write", ttl, ttl%interval, interval)
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

// The completeness verdict is not readable on its own. WARMING says the window
// is short and stops; whether it is short by one point, which the next round
// fixes, or short by twelve because the series does not live long enough to
// ever fill it, is the whole question -- and the two produce the identical
// verdict on every round, for ever.
//
// Summarize is the only place that holds both numbers. It used to return one.
func TestWindowSummaryReportsWhatItJudgedTheCountAgainst(t *testing.T) {
	const (
		required = 9
		interval = time.Minute
	)
	fingerprint := strings.Repeat("b", 64)
	requirement := NewLevelRequirement(
		execution.StateRetentionRequirement{LevelID: 1, RetentionPoints: required, EvaluationInterval: interval},
		fingerprint, required,
	)
	window, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	const latest = int64(1_700_000_000)
	// Two points against a nine-point requirement: the shape of a series whose
	// lifetime is shorter than the window it is being judged in.
	points := []StatePoint{
		{RecordID: fmt.Sprintf("%064d", 1), SourceTime: latest - int64(interval/time.Second),
			Levels: []PointLevelFact{{LevelID: 1, DetectFingerprint: fingerprint, Result: LevelFactNormal}}},
		{RecordID: fmt.Sprintf("%064d", 2), SourceTime: latest,
			Levels: []PointLevelFact{{LevelID: 1, DetectFingerprint: fingerprint, Result: LevelFactNormal}}},
	}
	if _, err = window.Apply(points); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	view, ok := window.History(1)
	if !ok {
		t.Fatal("Level history is missing")
	}
	summary := view.Summarize(latest, required)
	if summary.Completeness != HistoryWarming {
		t.Fatalf("completeness = %q, want the fixture to be short", summary.Completeness)
	}
	if summary.ValidPositions != 2 {
		t.Fatalf("valid positions = %d, want 2", summary.ValidPositions)
	}
	if summary.RequiredPositions != required {
		t.Fatalf("required positions = %d, want %d: without it the shortfall cannot be computed "+
			"anywhere downstream, and every short window reads the same",
			summary.RequiredPositions, required)
	}
	// And when the caller passes nothing, the requirement the Level carries is
	// what was actually used -- so that is what has to be reported, not zero.
	if got := view.Summarize(latest, 0).RequiredPositions; got != required {
		t.Fatalf("required positions = %d with a defaulted requirement, want %d", got, required)
	}
}

// A series whose data stops does not stay GAPPED. It runs FULL, then GAPPED
// while its last real point is still inside the window, then WARMING for ever
// once that point slides out -- because with no valid position at all, every
// missing one counts as missing before the first, which is the WARMING arm.
//
// This is why a short window cannot be read as "the series has not lived long
// enough": it is also where a dead series ends up, and the two report the same
// verdict for ever. The count of empty windows is what separates them, and
// this test is the evidence that the second case exists at all.
func TestASeriesWhoseDataStopsEndsUpWarmingNotGapped(t *testing.T) {
	const required = 5
	const interval = time.Minute
	fingerprint := strings.Repeat("d", 64)
	requirement := NewLevelRequirement(
		execution.StateRetentionRequirement{LevelID: 1, RetentionPoints: required, EvaluationInterval: interval},
		fingerprint, required,
	)
	window, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	const base = int64(1_700_000_000)
	step := int64(interval / time.Second)
	points := make([]StatePoint, required)
	for index := range points {
		points[index] = StatePoint{
			RecordID:   fmt.Sprintf("%064d", index),
			SourceTime: base + int64(index)*step,
			Levels:     []PointLevelFact{{LevelID: 1, DetectFingerprint: fingerprint, Result: LevelFactNormal}},
		}
	}
	if _, err = window.Apply(points); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	view, ok := window.History(1)
	if !ok {
		t.Fatal("Level history is missing")
	}
	last := base + int64(required-1)*step

	if got := view.Summarize(last, required).Completeness; got != HistoryFull {
		t.Fatalf("completeness at the last point = %q, want FULL", got)
	}
	// While the last real point is still inside the window.
	for silent := int64(1); silent < required; silent++ {
		summary := view.Summarize(last+silent*step, required)
		if summary.Completeness != HistoryGapped {
			t.Errorf("%d minutes of silence: completeness = %q, want GAPPED while the last point "+
				"is still in the window", silent, summary.Completeness)
		}
		if summary.ValidPositions == 0 {
			t.Errorf("%d minutes of silence: valid positions = 0 while still GAPPED", silent)
		}
	}
	// And once it has slid out.
	for silent := int64(required); silent <= required+2; silent++ {
		summary := view.Summarize(last+silent*step, required)
		if summary.Completeness != HistoryWarming {
			t.Errorf("%d minutes of silence: completeness = %q, want WARMING once the last point "+
				"has left the window", silent, summary.Completeness)
		}
		if summary.ValidPositions != 0 {
			t.Errorf("%d minutes of silence: valid positions = %d, want 0 -- that zero is the only "+
				"thing separating this from a series that has not lived long enough",
				silent, summary.ValidPositions)
		}
	}
}

// TestAReturningSeriesMeetsItsKeyAliveOnBothSidesOfTheWrite is the mechanism
// the half-step offset exists for, as a timeline.
//
// A Slot writes at a fixed phase inside its step. A series that stops
// reporting leaves a key written at that phase, and when it returns after
// exactly the TTL's worth of rounds the returning Slot reads a few seconds
// before that phase and writes a few seconds after it. With the TTL a whole
// number of steps the key expires at the phase itself: found at the read, gone
// at the write, and the round fails as a version conflict that costs the Slot a
// retry. With the expiry half a step away the key is alive on both sides of
// the write for any Slot whose read-to-write span is under half a step, and a
// series returning one round later than that meets no key at all, which is the
// clean restart the TTL was always meant to produce.
func TestAReturningSeriesMeetsItsKeyAliveOnBothSidesOfTheWrite(t *testing.T) {
	const (
		step          = time.Minute
		restartMargin = 10 * time.Minute
		writePhase    = 42 * time.Second // the phase the Slot writes at
		readToWrite   = 9 * time.Second  // the widest span measured on the deployment
	)
	fingerprint := strings.Repeat("a", 64)
	requirement := NewLevelRequirement(
		execution.StateRetentionRequirement{LevelID: 1, RetentionPoints: 1, EvaluationInterval: step},
		fingerprint, 1,
	)
	ttl, err := StateTTL([]LevelRequirement{requirement}, restartMargin, time.Minute, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	lastWrite := writePhase
	expiry := lastWrite + ttl
	// The round the series returns in is the one whose write phase is the
	// last whole step at or before the expiry.
	returning := expiry - expiry%step + writePhase
	if returning > expiry {
		returning -= step
	}
	read, write := returning-readToWrite, returning
	if !(read < expiry) {
		t.Fatalf("the returning Slot's read at %s is after the expiry at %s; the shape under test needs the key found at the read", read, expiry)
	}
	if !(write < expiry) {
		t.Fatalf("the key written at %s with TTL %s expires at %s, inside the returning Slot's read-to-write window [%s, %s]: "+
			"found at the read, gone at the write, a version conflict for the clock's sake", lastWrite, ttl, expiry, read, write)
	}
	// One round later there is nothing to find, on either side of the write.
	if late := returning + step - readToWrite; late < expiry {
		t.Fatalf("a series returning one round later still finds its key at %s (expiry %s); the TTL outlives a whole extra round", late, expiry)
	}
}

// The offset only ever moves a TTL forward. A base that already sits past
// the half step is carried to the next half step, never pulled back to this
// one: pulling back would put the expiry before the horizon the TTL was
// derived to outlive.
func TestTheHalfStepOffsetNeverShortensATTL(t *testing.T) {
	step := time.Minute
	requirement := []LevelRequirement{{EvaluationInterval: step}}
	for _, tc := range []struct {
		name string
		base time.Duration
		want time.Duration
	}{
		{"on the step", 11 * time.Minute, 11*time.Minute + 30*time.Second},
		{"already half a step off", 11*time.Minute + 30*time.Second, 11*time.Minute + 30*time.Second},
		{"just short of half", 11*time.Minute + 29*time.Second, 11*time.Minute + 30*time.Second},
		{"past half", 11*time.Minute + 50*time.Second, 12*time.Minute + 30*time.Second},
		{"one second before the next step", 11*time.Minute + 59*time.Second, 12*time.Minute + 30*time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := offsetFromTheStep(tc.base, requirement)
			if got != tc.want {
				t.Fatalf("offsetFromTheStep(%s) = %s, want %s", tc.base, got, tc.want)
			}
			if got < tc.base {
				t.Fatalf("offsetFromTheStep(%s) = %s shortened the TTL", tc.base, got)
			}
			if got%step != step/2 {
				t.Fatalf("offsetFromTheStep(%s) = %s is not half a step off", tc.base, got)
			}
		})
	}
	// No step, no offset: there is no phase to stay away from.
	if got := offsetFromTheStep(11*time.Minute, nil); got != 11*time.Minute {
		t.Fatalf("offsetFromTheStep with no requirement = %s, want the input unchanged", got)
	}
}

// The offset survives both bounds, through StateTTL itself rather than the
// helper. A floor that clamps a short retention up to a whole minute used to
// land the TTL back on the step; a ceiling that the retention fit exactly used
// to be pushed over and refused, and a Plan refused here is a Plan that stops
// remembering. Under the ceiling the TTL steps back half a step instead.
func TestTheHalfStepOffsetSurvivesTheFloorAndTheCeiling(t *testing.T) {
	step := time.Minute
	fingerprint := strings.Repeat("a", 64)
	requirementOf := func(points uint32) []LevelRequirement {
		return []LevelRequirement{NewLevelRequirement(
			execution.StateRetentionRequirement{LevelID: 1, RetentionPoints: points, EvaluationInterval: step},
			fingerprint, points)}
	}
	t.Run("clamped up to the floor", func(t *testing.T) {
		// One point and no margin derive one minute; the floor is five.
		ttl, err := StateTTL(requirementOf(1), 0, 5*time.Minute, 30*24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if ttl != 5*time.Minute+30*time.Second {
			t.Fatalf("TTL = %s, want the floor moved half a step off the phase (5m30s)", ttl)
		}
	})
	t.Run("fits the ceiling exactly", func(t *testing.T) {
		// Eleven points and no margin derive eleven minutes; the ceiling is eleven.
		ttl, err := StateTTL(requirementOf(11), 0, time.Minute, 11*time.Minute)
		if err != nil {
			t.Fatalf("a retention that fits the ceiling was refused: %v", err)
		}
		if ttl != 10*time.Minute+30*time.Second {
			t.Fatalf("TTL = %s, want the previous half step under the ceiling (10m30s)", ttl)
		}
		if ttl%step != step/2 {
			t.Fatalf("TTL %s is not half a step off", ttl)
		}
	})
	t.Run("over the ceiling is still refused", func(t *testing.T) {
		if _, err := StateTTL(requirementOf(12), 0, time.Minute, 11*time.Minute); !errors.Is(err, ErrStateBudget) {
			t.Fatalf("StateTTL() error = %v, want the budget refusal", err)
		}
	})
	t.Run("ceiling too tight to step back stays put", func(t *testing.T) {
		// Derived two minutes, floor two minutes, ceiling two minutes: neither
		// half step fits, and the bounds win over the phase.
		ttl, err := StateTTL(requirementOf(2), 0, 2*time.Minute, 2*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if ttl != 2*time.Minute {
			t.Fatalf("TTL = %s, want the bounds' 2m0s when no half step fits between them", ttl)
		}
	})
}
