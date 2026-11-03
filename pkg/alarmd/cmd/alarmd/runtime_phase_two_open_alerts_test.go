// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The replica's published facts about its copy: before any read no age,
// the reader's own version and every answer word at zero; once read and
// calibrated, each age as the seconds since its own time. The port adapter
// turns the Plans the worker names into the strategy keys the copy tracks.
func TestOpenAlertSetFactsAndPortAdapter(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return at }
	cache, err := openalerts.NewIndex(openalerts.IndexOptions{Source: nothingIndexed{}, Subscriber: nothingIndexed{}, Now: now,
		MaxStrategies: 4, MaxMembers: 4, MaxBytes: 1 << 16, MaxLocalEntries: 4, ReadBatch: 1, ReconcileBatch: 1,
		RefreshInterval: time.Minute, IndexInterval: time.Minute, ReconcileInterval: time.Minute, CalibrationMaxAge: time.Minute,
		LocalRetention: time.Minute, CycleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	facts := openAlertSetFactsSource(cache, now)()
	if facts == nil || facts.StaleBeyondBound || facts.IndexReadAgeSeconds != nil || facts.AuthoritativeAgeSeconds != nil {
		t.Fatalf("facts before any read = %+v, want not stale, no age", facts)
	}
	if facts.Available || facts.TrackedSets != 0 || facts.Members != 0 {
		t.Fatalf("account before any read = %+v, want nothing read", facts)
	}
	for _, answer := range openalerts.Answers {
		if count, present := facts.Lookups[string(answer)]; !present || count != 0 {
			t.Fatalf("lookups before any lookup = %v, want every answer word at zero", facts.Lookups)
		}
	}

	port := openAlertCopyPort{cache: cache}
	port.TrackPlans("qg-test", []execution.PlanIdentity{{TenantID: "default", BusinessID: "2", StrategyID: "1001"}})
	if stats := cache.Stats(); stats.Tracked != 1 {
		t.Fatalf("tracked = %d, want the one Plan the worker named", stats.Tracked)
	}

	read := openAlertSetFacts(openalerts.Stats{IndexReadAt: at.Add(-45 * time.Second), LoadedAt: at.Add(-90 * time.Second)}, false, at)
	if read.IndexReadAgeSeconds == nil || *read.IndexReadAgeSeconds != 45 || read.AuthoritativeAgeSeconds == nil || *read.AuthoritativeAgeSeconds != 90 {
		t.Fatalf("ages after a read = %v and %v, want 45 since the read and 90 since the calibration", read.IndexReadAgeSeconds, read.AuthoritativeAgeSeconds)
	}
}

type nothingIndexed struct{}

func (nothingIndexed) ReadSet(context.Context, openalerts.StrategyKey) ([]string, error) {
	return nil, nil
}

func (nothingIndexed) Watch(ctx context.Context, _ func(bool), _ func(openalerts.StrategyKey), _ func(openalerts.NoticeRefusal)) error {
	<-ctx.Done()
	return ctx.Err()
}

// The Console's two facts reach the published facts under their own names,
// and so the fleet verdict: the copy saying so is not enough if the replica
// does not pass it on. The keying's age is carried with it, and is absent
// while the keying was never read.
func TestTheConsoleFactsArePublishedWithTheReason(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	stats := openalerts.Stats{Configured: true, LocationConfirmed: false, UnavailableReason: openalerts.UnavailableLocationUnconfirmed}
	facts := openAlertSetFacts(stats, false, at)
	if !facts.Configured || facts.LocationConfirmed || facts.KeyedByAlertID != nil || facts.KeyedByAlertIDAsOf != nil ||
		facts.UnavailableReason != "location_unconfirmed" {
		t.Fatalf("facts = %+v, want configured, unconfirmed, keying absent and the reason named", facts)
	}
	keyed := true
	stats = openalerts.Stats{Configured: true, LocationConfirmed: true, KeyedByAlertID: &keyed, KeyedByAlertIDAsOf: at.Add(-time.Hour), Available: true}
	facts = openAlertSetFacts(stats, false, at)
	if !facts.LocationConfirmed || facts.KeyedByAlertID == nil || !*facts.KeyedByAlertID || facts.KeyedByAlertIDAsOf == nil ||
		!facts.KeyedByAlertIDAsOf.Equal(at.Add(-time.Hour)) || facts.UnavailableReason != "" {
		t.Fatalf("facts = %+v, want both confirmed with the keying's time", facts)
	}
}

// The gate's own-alert split and its kept lookups reach the replica's
// facts: every answer word in the own split, zero included, and the held
// lookups whole.
func TestTheGatesOwnHeldLookupsReachTheFacts(t *testing.T) {
	at := time.Date(2026, 9, 24, 4, 0, 0, 0, time.UTC)
	held := openalerts.GateLookup{At: at, TenantID: "system", StrategyID: "363", Fingerprint: "1f018838",
		Answer: openalerts.AnswerIndexAbsent, Own: true, InOtherSets: []string{"370"}}
	facts := openAlertSetFacts(openalerts.Stats{OwnLookups: map[openalerts.Answer]uint64{openalerts.AnswerIndexAbsent: 2},
		OwnHeld: 2, RecentLookups: []openalerts.GateLookup{held}, RecentOwnHeld: []openalerts.GateLookup{held}}, false, at)
	if facts.GateOwnHeld != 2 || facts.GateOwnLookups["index_absent"] != 2 || len(facts.GateOwnLookups) != len(openalerts.Answers) {
		t.Fatalf("own held %d own lookups %v", facts.GateOwnHeld, facts.GateOwnLookups)
	}
	if len(facts.GateRecentOwnHeld) != 1 || facts.GateRecentOwnHeld[0].Fingerprint != "1f018838" || facts.GateRecentOwnHeld[0].InOtherSets[0] != "370" ||
		!facts.GateRecentOwnHeld[0].Own || facts.GateRecentOwnHeld[0].Answer != "index_absent" || len(facts.GateRecent) != 1 {
		t.Fatalf("kept lookups %+v %+v", facts.GateRecentOwnHeld, facts.GateRecent)
	}
}

// The departures reach the facts with every path word, and own_open is
// carried as a number, zero included, which is an answer.
func TestTheDeparturesAndOwnOpenReachTheFacts(t *testing.T) {
	at := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	facts := openAlertSetFacts(openalerts.Stats{SentDepartures: map[string]uint64{openalerts.DepartureNotResent: 3},
		OwnOpen: 0, OwnOpenDepartures: map[string]uint64{openalerts.DepartureNotInSet: 1, openalerts.DepartureEvicted: 2}}, false, at)
	if facts.SentDepartures["not_resent"] != 3 || len(facts.SentDepartures) != len(openalerts.SentDepartures) {
		t.Fatalf("sent departures %v", facts.SentDepartures)
	}
	if facts.OwnOpen == nil || *facts.OwnOpen != 0 || facts.OwnOpenDepartures["evicted"] != 2 ||
		facts.OwnOpenDepartures["not_in_set"] != 1 || len(facts.OwnOpenDepartures) != len(openalerts.OwnOpenDepartures) {
		t.Fatalf("own open %v departures %v", facts.OwnOpen, facts.OwnOpenDepartures)
	}
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"own_open":0`) {
		t.Fatalf("a known zero own_open was dropped: %s", encoded)
	}
}

// The bound a set's last read is judged stale against reaches the replica's
// facts beside the read's age, with how many sets are past it: the age and
// the bound read in one step, on any deployment, whatever its strategies.
func TestTheStaleBoundIsPublishedBesideTheReadAge(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	facts := openAlertSetFacts(openalerts.Stats{IndexReadAt: at.Add(-400 * time.Second), StaleAfter: 336 * time.Second, Stale: 2}, false, at)
	if facts.IndexReadAgeSeconds == nil || *facts.IndexReadAgeSeconds != 400 || facts.IndexStaleAfterSeconds != 336 || facts.StaleSets != 2 {
		t.Fatalf("facts = age %v, bound %v, stale %d; want 400 against 336 with two stale", facts.IndexReadAgeSeconds, facts.IndexStaleAfterSeconds, facts.StaleSets)
	}
}
