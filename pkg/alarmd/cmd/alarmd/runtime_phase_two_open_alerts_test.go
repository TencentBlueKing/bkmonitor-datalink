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
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

type staticOpenAlertSource struct{ publication openalerts.Publication }

func (source staticOpenAlertSource) Read(context.Context, []openalerts.StrategyKey) (openalerts.Publication, error) {
	return source.publication, nil
}

// The replica's published facts about its copy: the age is absent until a
// publication has been read, and present as the seconds since once it has;
// the mode and the stale flag are the copy's own. The port adapter turns
// the Plans the worker names into the strategy keys the copy reads.
func TestOpenAlertSetFactsAndPortAdapter(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return at }
	source := &staticOpenAlertSource{}
	cache, err := openalerts.New(openalerts.Options{Source: source, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	facts := openAlertSetFactsSource(cache, now)()
	if facts == nil || facts.Mode != string(openalerts.ModeNeverLoaded) || facts.StaleBeyondBound || facts.AuthoritativeAgeSeconds != nil {
		t.Fatalf("facts before any read = %+v, want never_loaded, not stale, no age", facts)
	}
	// The reader's account before any read: no heartbeat and so no age,
	// cycle or publisher version -- not zeros; the reader's own version;
	// every answer word present at zero.
	if facts.Available || facts.HeartbeatAgeSeconds != nil || facts.CycleSeconds != 0 || facts.FingerprintVersion != "" ||
		facts.ReaderFingerprintVersion != openalerts.FingerprintVersion || facts.TrackedSets != 0 || facts.Members != 0 {
		t.Fatalf("account before any read = %+v, want nothing of the publisher and the reader's own version", facts)
	}
	for _, answer := range openalerts.Answers {
		if count, present := facts.Lookups[string(answer)]; !present || count != 0 {
			t.Fatalf("lookups before any lookup = %v, want every answer word at zero", facts.Lookups)
		}
	}
	if facts.Comparison != nil {
		t.Fatalf("a copy that does not read the index has nothing to compare: %+v", facts.Comparison)
	}

	port := openAlertCopyPort{cache: cache}
	port.TrackPlans("qg-test", []execution.PlanIdentity{{TenantID: "default", BusinessID: "2", StrategyID: "1001"}})
	source.publication = openalerts.Publication{
		Heartbeat: &openalerts.Heartbeat{PublishedAt: at, Cycle: time.Minute, FingerprintVersion: openalerts.FingerprintVersion},
		Sets:      map[openalerts.StrategyKey][]string{{TenantID: "default", StrategyID: "1001"}: {"f1"}},
	}
	cache.Refresh(context.Background())
	if !port.Contains("default", "1001", "f1") {
		t.Fatal("the tracked strategy's set was not read through the port")
	}
	at = at.Add(45 * time.Second)
	facts = openAlertSetFactsSource(cache, now)()
	if facts.Mode != string(openalerts.ModeAuthoritative) || facts.AuthoritativeAgeSeconds == nil || *facts.AuthoritativeAgeSeconds != 45 {
		t.Fatalf("facts after a read = %+v, want authoritative with age 45", facts)
	}
	if stats := cache.Stats(); stats.Tracked != 1 {
		t.Fatalf("tracked = %d, want the one Plan the worker named", stats.Tracked)
	}
	// The account after the read: the publisher's heartbeat by its own
	// clock, its cycle and version, one set tracked and loaded with its one
	// member, and the lookup above counted under the authoritative word.
	if !facts.Available || facts.UnavailableReason != "" || facts.HeartbeatAgeSeconds == nil || *facts.HeartbeatAgeSeconds != 45 ||
		facts.CycleSeconds != 60 || facts.FingerprintVersion != openalerts.FingerprintVersion ||
		facts.TrackedSets != 1 || facts.LoadedSets != 1 || facts.Members != 1 || facts.Lookups[string(openalerts.AnswerMember)] != 1 {
		t.Fatalf("account after a read = %+v, want available, heartbeat 45 s old, cycle 60, one set with one member, one authoritative_member lookup", facts)
	}
	// The publisher stops: past the staleness bound the copy is on its own
	// and the account says why, with the last heartbeat still dated.
	source.publication = openalerts.Publication{}
	at = at.Add(10 * time.Minute)
	cache.Refresh(context.Background())
	facts = openAlertSetFactsSource(cache, now)()
	if facts.Available || facts.Mode != string(openalerts.ModeSelfMaintained) || facts.UnavailableReason != string(openalerts.UnavailableHeartbeatMissing) ||
		facts.HeartbeatAgeSeconds == nil || *facts.HeartbeatAgeSeconds != 645 {
		t.Fatalf("account after the publisher stopped = %+v, want self_maintained for heartbeat_missing with the last heartbeat 645 s old", facts)
	}
}

// Every field of the copy's comparison reaches the replica's facts: a field
// added to one side and not carried would read as zero, and a zero here is a
// reading ("none of the alerts is ours").
func TestTheOpenAlertComparisonIsCarriedFieldForField(t *testing.T) {
	comparison := &openalerts.Comparison{OwnEventSourceID: "own", Sent: 3,
		SentShapes: map[string]int{openalerts.ShapeHex32: 3}, MemberShapes: map[string]int{openalerts.ShapeHex64: 5},
		AlertSources: map[string]int{"own": 0, "elsewhere": 5}, SentInCalibrated: 3, SentMatchingAlertID: 2, SentMatchingFingerprint: 1,
		Strategies: []openalerts.ComparisonStrategy{{TenantID: "t", StrategyID: "s", Sent: 1, Members: 2, Alerts: 3, Calibrated: true,
			SentSample: []string{"a"}, MemberSample: []string{"b"},
			AlertSample: []openalerts.ComparisonAlert{{AlertID: "c", Fingerprint: "d", EventSourceID: "e"}}}}}
	facts := openAlertComparisonFacts(comparison)
	var zero func(path string, value reflect.Value)
	zero = func(path string, value reflect.Value) {
		switch value.Kind() {
		case reflect.Ptr:
			zero(path, value.Elem())
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				zero(path+"."+value.Type().Field(i).Name, value.Field(i))
			}
		case reflect.Slice:
			if value.Len() == 0 {
				t.Errorf("%s was not carried", path)
			}
			for i := 0; i < value.Len(); i++ {
				zero(path, value.Index(i))
			}
		default:
			if value.IsZero() {
				t.Errorf("%s was not carried", path)
			}
		}
	}
	zero("comparison", reflect.ValueOf(facts))
	if openAlertComparisonFacts(nil) != nil {
		t.Error("no comparison is carried as none")
	}
}

type nothingIndexed struct{}

func (nothingIndexed) ReadSet(context.Context, openalerts.StrategyKey) ([]string, error) {
	return nil, nil
}

func (nothingIndexed) Watch(ctx context.Context, _ func(bool), _ func(openalerts.StrategyKey)) error {
	<-ctx.Done()
	return ctx.Err()
}

// A copy that reads the index publishes its comparison in the replica's
// facts, before any read as well: the counts are then zeros, which is what
// the copy knows.
func TestAnIndexCopyPublishesItsComparison(t *testing.T) {
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	cache, err := openalerts.NewIndex(openalerts.IndexOptions{Source: nothingIndexed{}, Subscriber: nothingIndexed{}, Now: now,
		MaxStrategies: 1, MaxMembers: 1, MaxBytes: 1, MaxLocalEntries: 1, ReadBatch: 1, ReconcileBatch: 1,
		RefreshInterval: time.Minute, IndexInterval: time.Minute, ReconcileInterval: time.Minute, CalibrationMaxAge: time.Minute,
		LocalRetention: time.Minute, CycleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	facts := openAlertSetFactsSource(cache, now)()
	if facts.Comparison == nil || facts.Comparison.SentShapes == nil || facts.Comparison.MemberShapes == nil {
		t.Fatalf("the index copy's facts carry no comparison: %+v", facts.Comparison)
	}
}

// Sets that carry none of this replica's alerts reach the published facts
// under their own names, and so the fleet verdict: the copy saying so is
// not enough if the replica does not pass it on.
func TestTheDisjointStateIsPublishedWithTheSentCounts(t *testing.T) {
	stats := openalerts.Stats{Mode: openalerts.ModeSelfMaintained, IndexProtocol: true,
		SentInSet: 0, SentNotInSet: 103, Disjoint: true, UnavailableReason: openalerts.UnavailableMembersDisjoint}
	facts := openAlertSetFacts(stats, false, time.Unix(1_700_000_000, 0))
	if !facts.Disjoint || facts.SentInSet != 0 || facts.SentNotInSet != 103 || facts.UnavailableReason != "members_disjoint" {
		t.Fatalf("facts = %+v, want disjoint with 0 of 103 found and the reason named", facts)
	}
	stats.SentInSet, stats.Disjoint, stats.UnavailableReason = 4, false, ""
	if facts := openAlertSetFacts(stats, false, time.Unix(1_700_000_000, 0)); facts.Disjoint || facts.SentInSet != 4 {
		t.Fatalf("facts = %+v, want the found count and no disjoint", facts)
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
