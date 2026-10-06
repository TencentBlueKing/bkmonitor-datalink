// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	sentKey  = "0123456789abcdef0123456789abcdef"
	longKey  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	otherKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// comparisonAfter calibrates keyA against what the link answers, then sends
// one ABNORMAL for sentKey, and returns the comparison.
func comparisonAfter(t *testing.T, answer Reconciliation) *Comparison {
	t.Helper()
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return answer.Members, nil })
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) { return answer, nil })
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	if !cache.Snapshot(keyA).Calibrated {
		t.Fatal("the fixture did not calibrate")
	}
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, sentKey)})
	return cache.Comparison()
}

// The three ways a gate can hold a calibrated set and never find its own
// alerts each read differently side by side, and the healthy case reads as a
// match.
func TestTheComparisonTellsTheThreeMissesApart(t *testing.T) {
	// The link fingerprints on a field list: our alert is there, by id, under
	// a 64-character member.
	fields := comparisonAfter(t, Reconciliation{EventSourceID: "own", Members: []string{longKey},
		Alerts: []Alert{{AlertID: sentKey, EventSourceID: "own", Fingerprint: longKey}}})
	if fields.MemberShapes[ShapeHex64] != 1 || fields.SentShapes[ShapeHex32] != 1 ||
		fields.SentMatchingAlertID != 1 || fields.SentMatchingFingerprint != 0 {
		t.Errorf("another fingerprint rule reads as our alert under another member: %+v", fields)
	}

	// The set holds another source's alerts and none of ours.
	foreign := comparisonAfter(t, Reconciliation{EventSourceID: "own", Members: []string{otherKey},
		Alerts: []Alert{{AlertID: otherKey, EventSourceID: "elsewhere", Fingerprint: otherKey}}})
	if foreign.AlertSources["own"] != 0 || foreign.AlertSources["elsewhere"] != 1 || foreign.OwnEventSourceID != "own" ||
		foreign.SentMatchingAlertID != 0 || foreign.SentMatchingFingerprint != 0 {
		t.Errorf("another source's alerts read as not ours, the own source listed at zero: %+v", foreign)
	}
	if _, listed := foreign.AlertSources["own"]; !listed {
		t.Errorf("the own source is listed even with no alert: %v", foreign.AlertSources)
	}

	// The link holds our alert under the key we sent.
	match := comparisonAfter(t, Reconciliation{EventSourceID: "own", Members: []string{sentKey},
		Alerts: []Alert{{AlertID: sentKey, EventSourceID: "own", Fingerprint: sentKey}}})
	if match.SentInCalibrated != 1 || match.SentMatchingFingerprint != 1 || match.AlertSources["own"] != 1 {
		t.Errorf("a set holding our alert reads as a match: %+v", match)
	}
	row := match.Strategies[0]
	if row.StrategyID != keyA.StrategyID || row.TenantID != keyA.TenantID || !row.Calibrated || row.Sent != 1 ||
		row.Members != 1 || row.Alerts != 1 || row.SentSample[0] != sentKey[:8] || row.MemberSample[0] != sentKey[:8] ||
		row.AlertSample[0].Fingerprint != sentKey[:8] || row.AlertSample[0].AlertID != sentKey[:8] {
		t.Errorf("the strategy's row: %+v", row)
	}
}

// Everything the comparison carries is bounded whatever the link or this
// process holds, and a sample never carries a whole key.
func TestTheComparisonIsBounded(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.MaxLocalEntries = 1000
	var alerts []Alert
	var members []string
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("%032x", i)
		members = append(members, key)
		alerts = append(alerts, Alert{AlertID: key, EventSourceID: fmt.Sprintf("source-%02d", i), Fingerprint: key})
	}
	alerts = append(alerts, Alert{AlertID: "x", Fingerprint: "x"})
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return members, nil })
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		return Reconciliation{EventSourceID: "source-19", Members: members, Alerts: alerts}, nil
	})
	cache := mustIndex(t, options)
	var keys []StrategyKey
	for i := 0; i < 12; i++ {
		keys = append(keys, StrategyKey{TenantID: tenant, StrategyID: fmt.Sprintf("%d", 100+i)})
	}
	if err := cache.SetTracked(keys); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		cache.Refresh(context.Background())
	}
	var events []contract.TriggerEventV1
	for i := 0; i < 5; i++ {
		events = append(events, abnormal(keys[11], fmt.Sprintf("%032x", 100+i)))
	}
	cache.Acknowledged(events)

	got := cache.Comparison()
	if len(got.Strategies) != ComparisonStrategies {
		t.Fatalf("strategies %d, want %d", len(got.Strategies), ComparisonStrategies)
	}
	if got.Strategies[0].StrategyID != keys[11].StrategyID {
		t.Errorf("the strategy this process sent to comes first: %+v", got.Strategies[0])
	}
	for _, row := range got.Strategies {
		if len(row.SentSample) > ComparisonSamplesPerStrategy || len(row.MemberSample) > ComparisonSamplesPerStrategy ||
			len(row.AlertSample) > ComparisonSamplesPerStrategy {
			t.Errorf("unbounded sample: %+v", row)
		}
		for _, value := range append(append([]string(nil), row.SentSample...), row.MemberSample...) {
			if len(value) > 8 {
				t.Errorf("a sample carries a whole key: %q", value)
			}
		}
	}
	if len(got.AlertSources) > ComparisonSources+1 {
		t.Errorf("sources %d past the bound: %v", len(got.AlertSources), got.AlertSources)
	}
	if _, own := got.AlertSources["source-19"]; !own {
		t.Errorf("the own source survives the bound: %v", got.AlertSources)
	}
	total := 0
	for _, n := range got.AlertSources {
		total += n
	}
	if calibrated := cache.Stats().Calibrated; total != calibrated*len(alerts) || got.AlertSources[ComparisonNoSource] != calibrated {
		t.Errorf("every calibrated alert is counted once, the sourceless under %q: %v (calibrated %d)",
			ComparisonNoSource, got.AlertSources, calibrated)
	}
	if got.Sent != 5 || !strings.HasPrefix(got.Strategies[0].SentSample[0], "00000000") {
		t.Errorf("sent %d, sample %v", got.Sent, got.Strategies[0].SentSample)
	}
}

func TestShapeWords(t *testing.T) {
	for value, want := range map[string]string{sentKey: ShapeHex32, longKey: ShapeHex64, "abc": ShapeHexOther,
		strings.ToUpper(sentKey): ShapeNonHex, "": ShapeNonHex, "0123456789abcdeg0123456789abcdef": ShapeNonHex} {
		if got := Shape(value); got != want {
			t.Errorf("Shape(%q) = %s, want %s", value, got, want)
		}
	}
}
