// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A series' runtime state lives at most H past its last write, never longer
// than its retention needs, and never shorter than a series that keeps
// reporting needs to reach its next write. The test retention is five points
// a minute apart with a minute of restart margin, so its own lifetime is a
// few minutes and its floor two.
func TestARuntimeLifetimeIsTheRetentionsCappedAtTheHorizonAndFlooredAtOneRound(t *testing.T) {
	store, _, _, uncapped := frozenRenewalFixture(t)
	floor := 2 * time.Minute
	for _, arm := range []struct {
		name    string
		horizon int64
		want    time.Duration
	}{
		{name: "no horizon: the retention's own", horizon: 0, want: uncapped},
		{name: "a horizon below the retention's lifetime caps it", horizon: 200, want: 200 * time.Second},
		{name: "a horizon above it lengthens nothing", horizon: 86400, want: uncapped},
		{name: "a horizon shorter than one round is floored at one round", horizon: 30, want: floor},
	} {
		t.Run(arm.name, func(t *testing.T) {
			got, err := store.runtimeTTL(testRetention(), arm.horizon)
			if err != nil {
				t.Fatal(err)
			}
			if got != arm.want {
				t.Fatalf("lifetime = %s, want %s (the retention's own is %s)", got, arm.want, uncapped)
			}
		})
	}
	if uncapped <= 200*time.Second {
		t.Fatalf("setup: the retention's own lifetime %s leaves no room above the horizon under test", uncapped)
	}
}

// The horizon on the request is the lifetime the write gives the key: the
// line that carries it from the apply request to the store's compare-and-set.
func TestAWriteGivesItsKeyTheCappedLifetime(t *testing.T) {
	store, backend, _, _ := frozenRenewalFixture(t)
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: stateIdentityV2(), ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"},
		ExpectedBlobRevision: 1,
		AffectedRecords:      []execution.RecordAnchor{derivedAnchor(t, stateIdentityV2(), 120)},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 120}},
		Points: []execution.StateHistoryPoint{derivedPoint(t, stateIdentityV2(), 120, "detect", execution.LevelFactNormal)},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.writeTTLs = nil
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: []execution.StateMutation{mutation}, HorizonSeconds: 200,
	})
	if err != nil || applied.Items[0].Status != execution.StateApplied {
		t.Fatalf("ApplyRuntime() = (%+v, %v), want the series written", applied, err)
	}
	for key, lifetime := range backend.writeTTLs {
		if lifetime != 200*time.Second {
			t.Fatalf("%s was written to live %s, want the 200 s horizon", key, lifetime)
		}
	}
	if len(backend.writeTTLs) == 0 {
		t.Fatal("the apply wrote nothing to check a lifetime on")
	}
}

// A frozen series is renewed to the capped lifetime too: renewal is the other
// way a key's life is set, and a renewal to the retention's own lifetime would
// keep a series' state past H the moment it froze.
func TestAFrozenRenewalGivesTheCappedLifetime(t *testing.T) {
	store, backend, key, _ := frozenRenewalFixture(t)
	request := frozenRequest(writtenAt().Add(190 * time.Second))
	request.HorizonSeconds = 200
	if _, err := store.RenewFrozenRuntime(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(backend.renewals) == 0 {
		t.Fatalf("the key was not renewed at 190 s of a 200 s life; nothing to check")
	}
	if got := backend.remaining[key]; got != 200*time.Second {
		t.Fatalf("renewed lifetime = %s, want the 200 s horizon", got)
	}
}
