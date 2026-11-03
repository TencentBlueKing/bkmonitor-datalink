// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A set too large for one read is counted under its own reason, apart from
// a read that failed: the first is the trigger for answering such a set one
// member at a time, the second an outage. A read refused for anything else
// - a member no alert id can be, a set that changed while it was read - is
// a failed read.
func TestASetTooLargeToReadIsCountedApartFromAFailedRead(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	var readErr error
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return nil, readErr })
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	readErr = ErrSetTooLarge
	cache.Refresh(context.Background())
	if stats := cache.Stats(); stats.Unavailable[UnavailableTooLarge] != 1 || stats.Unavailable[UnavailableReadError] != 0 {
		t.Fatalf("a set too large counted %v, want one too_large and no read_error", stats.Unavailable)
	}
	if snapshot := cache.Snapshot(keyA); snapshot.Reason != "index_too_large" {
		t.Fatalf("the strategy reads %q, want index_too_large", snapshot.Reason)
	}
	c.advance(2 * time.Hour)
	readErr = ErrIncomplete
	cache.Refresh(context.Background())
	if stats := cache.Stats(); stats.Unavailable[UnavailableTooLarge] != 1 || stats.Unavailable[UnavailableReadError] != 1 {
		t.Fatalf("an incomplete read counted %v, want it under read_error", stats.Unavailable)
	}
	if !errors.Is(ErrSetTooLarge, ErrIncomplete) {
		t.Fatal("a set too large is no longer an incomplete read to the callers that ask")
	}
}

// staleBoundFor is 3 x (IndexInterval + one pass over every tracked key) for
// the fixture's cadence: IndexInterval an hour, one key a cycle of a minute
// for up to four keys.
func staleBoundFor(keys int) time.Duration {
	return 3 * (time.Hour + time.Duration((keys+3)/4)*time.Minute)
}

// A set read once whose reads then keep failing answers from its last read
// only while that read is younger than the bound the refresh cadence sets.
// Past it the entry is stale and answers as one never read - the 09-14
// answer for a set that cannot be read: only what this process sent
// recently counts, and the pass-through policy lets everything go. A key
// never read answers self-maintained, apart from stale, so a cold start is
// told from a read outage.
func TestASetUnreadPastTheBoundStopsAnsweringFromItsLastRead(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	failing := false
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) {
		if failing {
			return nil, errors.New("read failed")
		}
		return []string{"fp"}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	readAt := c.at
	failing = true
	bound := staleBoundFor(1)

	c.at = readAt.Add(bound - time.Second)
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "fp") || cache.Contains(tenant, keyA.StrategyID, "other") {
		t.Fatal("inside the bound the last read is not what answers")
	}
	if snapshot := cache.Snapshot(keyA); snapshot.Stale || snapshot.StaleAfter != bound {
		t.Fatalf("inside the bound the strategy reads %+v, want not stale under a bound of %v", snapshot, bound)
	}

	c.at = readAt.Add(bound + time.Second)
	cache.Refresh(context.Background())
	before := cache.Stats().Lookups[AnswerIndexStale]
	if cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("past the bound the last read still answers")
	}
	if got := cache.Stats().Lookups[AnswerIndexStale]; got != before+1 {
		t.Fatalf("past the bound %d lookups answered stale, want one", got-before)
	}
	if snapshot := cache.Snapshot(keyA); !snapshot.Stale || snapshot.Reason != "index_stale" || snapshot.StaleAfter != bound {
		t.Fatalf("past the bound the strategy reads %+v, want stale under a bound of %v", snapshot, bound)
	}
	if stats := cache.Stats(); stats.Stale != 1 || stats.StaleAfter != bound {
		t.Fatalf("past the bound the copy reads %d stale under %v, want one under %v", stats.Stale, stats.StaleAfter, bound)
	}
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "sent")})
	if !cache.Contains(tenant, keyA.StrategyID, "sent") {
		t.Fatal("past the bound what this process sent recently does not count")
	}

	options.Policy = PolicyPassThrough
	passing := mustIndex(t, options)
	if err := passing.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	failing = false
	c.at = readAt
	passing.Refresh(context.Background())
	failing = true
	c.at = readAt.Add(bound + time.Second)
	passing.Refresh(context.Background())
	if !passing.Contains(tenant, keyA.StrategyID, "other") {
		t.Fatal("past the bound the pass-through policy does not let the recovery go")
	}

	never := mustIndex(t, indexOptions(c))
	never.index.options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return nil, errors.New("read failed") })
	if err := never.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	never.Refresh(context.Background())
	never.Contains(tenant, keyA.StrategyID, "fp")
	if stats := never.Stats(); stats.Lookups[AnswerSelfMaintained] != 1 || stats.Lookups[AnswerIndexStale] != 0 {
		t.Fatalf("a key never read answered %v, want self_maintained and not stale", stats.Lookups)
	}
}

// The bound grows with the keys a pass reads: at the same moment past one
// key's bound, a copy tracking nine keys - three cycles a pass - is still
// inside its own.
func TestTheStaleBoundGrowsWithTheKeysAPassReads(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	failing := false
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) {
		if failing {
			return nil, errors.New("read failed")
		}
		return []string{"fp"}, nil
	})
	cache := mustIndex(t, options)
	keys := []StrategyKey{keyA}
	for index := 0; index < 8; index++ {
		keys = append(keys, StrategyKey{TenantID: tenant, StrategyID: "more-" + string(rune('a'+index))})
	}
	if err := cache.SetTracked(keys); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 3; pass++ {
		cache.Refresh(context.Background())
	}
	readAt := c.at
	failing = true
	c.at = readAt.Add(staleBoundFor(1) + time.Minute)
	if !cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("nine keys past one key's bound: the larger copy's own bound was not used")
	}
	if snapshot := cache.Snapshot(keyA); snapshot.StaleAfter != staleBoundFor(9) {
		t.Fatalf("bound %v, want %v for nine keys", snapshot.StaleAfter, staleBoundFor(9))
	}
}

// Calibration is a read: a set whose index reads keep failing while its
// calibrations succeed is not stale, and answers from what the last
// calibration found. And a set calibrated within the calibration's own age
// - longer than the stale bound on a deployment - is still stale once both
// reads are past the bound, and answers as one never read: the pass-through
// policy lets the recovery go though the calibration has not expired.
func TestCalibrationKeepsASetFreshAndAStaleCalibratedSetFallsBackWhole(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Policy = PolicyPassThrough
	options.CalibrationMaxAge = 10 * time.Hour
	indexFails, calibrationFails := false, false
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) {
		if indexFails {
			return nil, errors.New("read failed")
		}
		return []string{"fp"}, nil
	})
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		if calibrationFails {
			return Reconciliation{}, errors.New("calibration failed")
		}
		return Reconciliation{Members: []string{"fp"}}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	readAt := c.at
	indexFails = true
	bound := staleBoundFor(1)

	// A calibration two hours on keeps the set fresh past the index read's
	// bound.
	c.at = readAt.Add(2*time.Hour + time.Second)
	cache.Refresh(context.Background())
	calibratedAt := cache.Snapshot(keyA).CalibratedAt
	if !calibratedAt.After(readAt) {
		t.Fatalf("no second calibration at %v (last %v)", c.at, calibratedAt)
	}
	c.at = readAt.Add(bound + time.Second)
	if snapshot := cache.Snapshot(keyA); snapshot.Stale || !cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatalf("a set kept fresh by calibration reads %+v and answers from nothing", snapshot)
	}

	// Both reads past the bound, the calibration within its own age.
	calibrationFails = true
	c.at = calibratedAt.Add(bound + time.Second)
	cache.Refresh(context.Background())
	if snapshot := cache.Snapshot(keyA); !snapshot.Stale || !snapshot.Calibrated {
		t.Fatalf("both reads past the bound with a calibration in its age: %+v, want stale and still calibrated", snapshot)
	}
	if !cache.Contains(tenant, keyA.StrategyID, "other") {
		t.Fatal("a stale, calibrated set did not fall back to the pass-through policy")
	}
}
