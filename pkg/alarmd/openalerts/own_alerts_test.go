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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// recoveryAt is a RECOVERY envelope for one Level, the others undecided.
func recoveryAt(key StrategyKey, fingerprint string, level uint32) contract.TriggerEventV1 {
	event := recovery(key, fingerprint)
	event.LevelResults = []contract.LevelResultV1{{LevelID: level, Priority: level, Result: contract.LevelResultRecovery}}
	return event
}

// While the sets are not trusted the gate answers from what this process
// opened (the 2026-09-14 ruling), and a RECOVERY does not take an alert out
// of that record at once, at any Level: the consumer closes an alert only at
// the Level it stands at, which the copy does not model. Every recovery of
// the alert goes out through a grace period, the calibration interval; one
// the consumer did not act on is an orphan there. Past the grace with no
// ABNORMAL since, the record lets the alert go, so that a lasting untrusted
// state does not grow the record without end. Before, a RECOVERY at another
// Level took the alert out at once and every later recovery was held.
func TestAnUntrustedSetKeepsAnOwnAlertThroughTheGraceAfterARecoveryAtAnyLevel(t *testing.T) {
	for _, level := range []uint32{1, 2} {
		t.Run(fmt.Sprintf("level %d", level), func(t *testing.T) {
			facts := confirmedFacts()
			facts.set(false, true, true)
			f := newSetFixture(t, PolicySelfMaintain, facts)
			grace := f.cache.index.options.ReconcileInterval
			f.send("ours")
			f.cache.Acknowledged([]contract.TriggerEventV1{recoveryAt(keyA, "ours", level)})
			for elapsed := time.Duration(0); elapsed < grace; elapsed += 10 * time.Minute {
				if !f.cache.Contains(tenant, keyA.StrategyID, "ours") {
					t.Fatalf("after %s: an alert this process opened answered not open inside the grace %s", elapsed, grace)
				}
				f.reread(10 * time.Minute)
			}
			// The clock is at the RECOVERY plus exactly the grace: still held.
			if !f.cache.Contains(tenant, keyA.StrategyID, "ours") || f.cache.Stats().OwnOpenDepartures[DepartureRecovered] != 0 {
				t.Fatalf("at the grace: let go (departures %v), want it held to the end of the grace", f.cache.Stats().OwnOpenDepartures)
			}
			f.reread(time.Second)
			if f.cache.Contains(tenant, keyA.StrategyID, "ours") || f.cache.Stats().OwnOpenDepartures[DepartureRecovered] != 1 {
				t.Fatalf("a second past the grace: still open, departures %v, want it gone as recovered", f.cache.Stats().OwnOpenDepartures)
			}
		})
	}
}

// An ABNORMAL after a RECOVERY is the alert open again: the grace starts
// over from the next RECOVERY, not from the first. Read past the earlier
// grace and past the recent sends' retention, so the record alone answers.
func TestAnAbnormalAfterARecoveryClearsTheGrace(t *testing.T) {
	facts := confirmedFacts()
	facts.set(false, true, true)
	f := newSetFixture(t, PolicySelfMaintain, facts)
	grace := f.cache.index.options.ReconcileInterval
	f.send("ours")
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "ours")})
	f.reread(grace - time.Minute)
	f.send("ours")
	f.reread(f.cache.index.options.LocalRetention + 2*time.Minute)
	if !f.cache.Contains(tenant, keyA.StrategyID, "ours") || f.cache.Stats().OwnOpenDepartures[DepartureRecovered] != 0 {
		t.Fatalf("an alert sent ABNORMAL again was let go on the grace of an earlier recovery (departures %v)",
			f.cache.Stats().OwnOpenDepartures)
	}
}

// A lasting untrusted state with alerts opening and recovering keeps the
// record to the open alerts and those recovered within the grace: nothing
// is evicted for want of room, and each recovered alert answers open - its
// recovery going out as an orphan - for at most grace / period rounds. The
// round that the gate lets through sends the RECOVERY again, as the trigger
// does, and that resend does not move the grace on.
func TestALastingUntrustedStateDoesNotFillTheRecord(t *testing.T) {
	facts := confirmedFacts()
	facts.set(true, false, true)
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Facts, options.MaxLocalEntries, options.ReconcileInterval = facts, 20, 10*time.Minute
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	period := time.Minute
	maxOrphans := int(options.ReconcileInterval/period) + 1
	orphans := map[string]int{}
	for round := 0; round < 200; round++ {
		open := fmt.Sprintf("alert-%d", round)
		cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, open)})
		if round > 0 {
			cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, fmt.Sprintf("alert-%d", round-1))})
		}
		for earlier := max(0, round-2*maxOrphans); earlier < round; earlier++ {
			name := fmt.Sprintf("alert-%d", earlier)
			if cache.Contains(tenant, keyA.StrategyID, name) {
				orphans[name]++
				if earlier < round-1 {
					cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, name)})
				}
			}
		}
		c.advance(period)
		cache.Refresh(context.Background())
	}
	stats := cache.Stats()
	if stats.OwnOpenDepartures[DepartureEvicted] != 0 {
		t.Fatalf("evicted %d, want none: the record filled", stats.OwnOpenDepartures[DepartureEvicted])
	}
	for name, n := range orphans {
		if n > maxOrphans {
			t.Fatalf("%s answered open %d rounds after its recovery, want at most %d", name, n, maxOrphans)
		}
	}
	if stats.OwnOpen > maxOrphans+1 {
		t.Fatalf("own open %d, want the open alert and those recovered within the grace only", stats.OwnOpen)
	}
}

// With the sets trusted the set is the answer: an alert it still carries
// after this process sent the RECOVERY is open - the consumer has not
// processed the RECOVERY, or took it as an orphan - and the next one goes
// out too; once the set lets the alert go, it is not open.
func TestATrustedSetAnswersAfterARecoveryWithoutHidingTheAlert(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts(), "ours")
	f.send("ours")
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "ours")})
	if !f.cache.Contains(tenant, keyA.StrategyID, "ours") {
		t.Fatal("the set still carries the alert and the gate held its recovery")
	}
	f.members = nil
	f.reread(f.cache.index.options.LocalRetention + time.Second)
	if f.cache.Contains(tenant, keyA.StrategyID, "ours") {
		t.Fatal("the set let the alert go and the gate still answered open")
	}
}

// The record of what this process opened shrinks by the set's word: a
// trusted calibration that lists an alert of ours nowhere - not as a
// member, missing or suppressed - takes it out, unless this process sent
// its ABNORMAL within the local retention before the calibration began (the
// consumer may not have published it yet). Missing from the set is not
// closed. An untrusted set prunes nothing.
func TestTheOpenedRecordIsPrunedByATrustedSetOnly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trusted    bool
		resend     bool
		late       bool
		missing    bool
		suppressed bool
		wantOwned  int
	}{
		{"trusted, not resent: pruned", true, false, false, false, false, 0},
		{"trusted, just resent: kept", true, true, false, false, false, 1},
		{"trusted, resent just past the retention: pruned", true, true, true, false, false, 0},
		{"trusted, listed missing: kept", true, false, false, true, false, 1},
		{"trusted, listed suppressed: kept", true, false, false, false, true, 1},
		{"untrusted: kept", false, false, false, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := confirmedFacts()
			c := &clock{at: time.Unix(1700000000, 0)}
			options := indexOptions(c)
			options.Facts = facts
			options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return []string{"theirs"}, nil })
			options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
				// The calibration takes two seconds: "just sent" is judged
				// against when it began, not when it ends.
				c.advance(2 * time.Second)
				if tc.missing {
					return Reconciliation{Members: []string{"theirs", "ours"}, Missing: []string{"ours"}}, nil
				}
				if tc.suppressed {
					return Reconciliation{Members: []string{"theirs"}, Suppressed: []string{"ours"}}, nil
				}
				return Reconciliation{Members: []string{"theirs"}}, nil
			})
			cache := mustIndex(t, options)
			if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
				t.Fatal(err)
			}
			cache.Refresh(context.Background())
			cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "ours")})
			if !tc.trusted {
				facts.set(true, false, true)
			}
			c.advance(options.LocalRetention + time.Second)
			if tc.resend {
				cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "ours")})
				// The recent sends give up their oldest under pressure: only
				// the record's own last send can keep the alert.
				cache.mu.Lock()
				delete(cache.added, member{key: keyA, fingerprint: "ours"})
				cache.mu.Unlock()
			}
			if tc.late {
				c.advance(options.LocalRetention + time.Second)
			} else {
				c.advance(options.LocalRetention - time.Second)
			}
			cache.indexChanged(keyA)
			cache.RequestReconcile(keyA)
			cache.Refresh(context.Background())
			stats := cache.Stats()
			if stats.OwnOpen != tc.wantOwned {
				t.Fatalf("own open %d, want %d (departures %v)", stats.OwnOpen, tc.wantOwned, stats.OwnOpenDepartures)
			}
			if want := uint64(1 - tc.wantOwned); stats.OwnOpenDepartures[DepartureNotInSet] != want {
				t.Fatalf("not_in_set %d, want %d", stats.OwnOpenDepartures[DepartureNotInSet], want)
			}
		})
	}
}

// Alerts that open and close over a long run do not fill the record: the
// trusted set's calibrations take out the closed ones, and a new alert is
// never evicted for want of room.
func TestTheOpenedRecordDoesNotFillUnderChurn(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts())
	limit := f.cache.index.options.MaxLocalEntries
	for i := 0; i < 5*limit; i++ {
		f.send(fmt.Sprintf("alert-%d", i))
		f.reread(f.cache.index.options.LocalRetention + time.Second)
	}
	if stats := f.cache.Stats(); stats.OwnOpenDepartures[DepartureEvicted] != 0 || stats.OwnOpenDepartures[DepartureNotInSet] != uint64(5*limit) {
		t.Fatalf("departures %v, want none evicted and every closed alert pruned", stats.OwnOpenDepartures)
	}
}
