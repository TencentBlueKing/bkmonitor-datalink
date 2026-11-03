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
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type clock struct{ at time.Time }

func (c *clock) now() time.Time          { return c.at }
func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

var (
	tenant = "default"
	keyA   = StrategyKey{TenantID: tenant, StrategyID: "1001"}
	keyB   = StrategyKey{TenantID: tenant, StrategyID: "1002"}
)

// abnormal and recovery are single-Level envelopes in the shape the
// evaluator publishes them: the Level's result rides with the kind, and the
// copy reads the result, as the consumer does.
func abnormal(key StrategyKey, fingerprint string) contract.TriggerEventV1 {
	return contract.TriggerEventV1{
		EventKind: contract.TriggerEventAbnormal, TenantID: key.TenantID, DedupeMD5: fingerprint,
		StrategyRef:  &contract.StrategySnapshotRef{TenantID: key.TenantID, Revision: 1},
		PlanRef:      contract.RuntimePlanRefV1{StrategyID: key.StrategyID},
		LevelResults: []contract.LevelResultV1{{LevelID: 1, Priority: 1, Result: contract.LevelResultAbnormal}},
	}
}

func recovery(key StrategyKey, fingerprint string) contract.TriggerEventV1 {
	event := abnormal(key, fingerprint)
	event.EventKind = contract.TriggerEventRecovery
	event.LevelResults = []contract.LevelResultV1{{LevelID: 1, Priority: 1, Result: contract.LevelResultRecovery}}
	return event
}

func wantLookups(t *testing.T, cache *Cache, want map[Answer]uint64) {
	t.Helper()
	got := cache.Stats().Lookups
	for _, answer := range Answers {
		if got[answer] != want[answer] {
			t.Fatalf("lookups = %v, want %v", got, want)
		}
	}
}

// A strategy the copy has not calibrated is answered by the policy; under
// pass-through every recovery goes, as before the gate existed.
func TestPassThroughPolicyLetsEveryRecoveryGoWhileUncalibrated(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	options := indexOptions(c)
	options.Policy = PolicyPassThrough
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "never-sent") {
		t.Fatal("pass-through answers open for everything while uncalibrated")
	}
	wantLookups(t, cache, map[Answer]uint64{AnswerPassedThrough: 1})
}

// Only envelopes the consumer will see move the copy.
func TestAcknowledgedIgnoresWhatTheConsumerNeverSees(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	cache := mustIndex(t, indexOptions(c))
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	legacy := abnormal(keyA, "legacy")
	legacy.LegacyOutput = &contract.LegacyEventContext{}
	noFingerprint := abnormal(keyA, "")
	noRevision := abnormal(keyA, "unfrozen")
	noRevision.StrategyRef = nil
	untracked := abnormal(keyB, "elsewhere")
	cache.Acknowledged([]contract.TriggerEventV1{legacy, noFingerprint, noRevision, untracked})
	if stats := cache.Stats(); stats.Added != 0 || stats.OwnOpen != 0 {
		t.Fatalf("added = %d own_open = %d, want 0 and 0", stats.Added, stats.OwnOpen)
	}
	cache.Acknowledged(nil)
}

// Own sends are bounded; past the bound the oldest go first and are counted.
func TestOwnSendsAreBoundedOldestFirst(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	options := indexOptions(c)
	options.MaxLocalEntries = 3
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "f"+strconv.Itoa(i))})
		c.advance(time.Second)
	}
	stats := cache.Stats()
	if stats.Added != 3 || stats.SentDepartures[DepartureEvicted] != 2 {
		t.Fatalf("added = %d evicted = %d, want 3 and 2", stats.Added, stats.SentDepartures[DepartureEvicted])
	}
	added := cache.Members(keyA)
	if len(added) != 3 || added[0] != "f2" || added[2] != "f4" {
		t.Fatalf("members = %v, want the newest three sends", added)
	}
}

func TestIndexOptionsAreValidated(t *testing.T) {
	if _, err := NewIndex(IndexOptions{}); err == nil {
		t.Fatal("sources and budgets are required")
	}
	options := indexOptions(&clock{at: time.Unix(1_700_000_000, 0)})
	options.Policy = "whatever"
	if _, err := NewIndex(options); err == nil {
		t.Fatal("an unknown policy is refused, not defaulted")
	}
}
