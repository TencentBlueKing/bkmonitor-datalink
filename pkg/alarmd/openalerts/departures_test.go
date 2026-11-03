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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func totalOwnLookups(stats Stats) uint64 {
	total := uint64(0)
	for _, n := range stats.OwnLookups {
		total += n
	}
	return total
}

// Each way out of the two records is counted by its path. A RECOVERY is not
// one: the consumer closes an alert only at the Level it stands at, and the
// set says which closed. not_resent is an alert no longer sent, which leaves
// the recent sends and stays the replica's own; not_in_set is an alert of
// ours a trusted set's calibration no longer lists.
func TestEveryDepartureFromWhatWasSentIsCountedByItsPath(t *testing.T) {
	ours := sentFingerprints(4)
	c := &clock{at: time.Unix(1700000000, 0)}
	// The consumer still holds three of ours; the fourth it closed.
	sets := map[StrategyKey][]string{keyA: {"theirs-1", ours[1], ours[2], ours[3]}, keyB: {"theirs-b", "ours-b"}}
	options := indexOptions(c)
	options.Source = setReaderFunc(func(_ context.Context, key StrategyKey) ([]string, error) { return sets[key], nil })
	options.Reconciler = reconcilerFunc(func(_ context.Context, key StrategyKey) (Reconciliation, error) {
		return Reconciliation{Members: append([]string(nil), sets[key]...)}, nil
	})
	f := &setFixture{c: c, cache: mustIndex(t, options)}
	if err := f.cache.SetTracked([]StrategyKey{keyA, keyB}); err != nil {
		t.Fatal(err)
	}
	f.cache.Refresh(context.Background())
	f.send(ours...)
	f.cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyB, "ours-b")})

	stats := f.cache.Stats()
	if stats.OwnOpen != 5 || stats.Added != 5 {
		t.Fatalf("after five sends own_open=%d added=%d, want 5 and 5", stats.OwnOpen, stats.Added)
	}

	// A RECOVERY the broker took changes neither record.
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, ours[0])})
	if again := f.cache.Stats(); again.OwnOpen != 5 || again.Added != 5 {
		t.Fatalf("a RECOVERY moved the records: own_open=%d added=%d", again.OwnOpen, again.Added)
	}

	// Past the retention the next calibration prunes the four from what was
	// sent, and the one the consumer closed from what this process opened.
	f.reread(options.LocalRetention + time.Second)
	stats = f.cache.Stats()
	if stats.SentDepartures[DepartureNotResent] != 4 || stats.OwnOpenDepartures[DepartureNotInSet] != 1 || stats.OwnOpen != 4 {
		t.Fatalf("after the calibration sent departures %v own departures %v own_open %d, want 4 not_resent, 1 not_in_set and 4 still open",
			stats.SentDepartures, stats.OwnOpenDepartures, stats.OwnOpen)
	}
	// The ones still held are still the replica's own at the gate.
	before := totalOwnLookups(stats)
	f.cache.Contains(tenant, keyA.StrategyID, ours[1])
	if after := totalOwnLookups(f.cache.Stats()); after != before+1 {
		t.Fatalf("an alert pruned from sent was not asked about as own: own lookups %d -> %d", before, after)
	}

	// Its strategy leaves: both records lose it, as untracked.
	if err := f.cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	stats = f.cache.Stats()
	wantSent := map[string]uint64{DepartureNotResent: 4, DepartureUntracked: 1, DepartureEvicted: 0}
	for path, n := range wantSent {
		if stats.SentDepartures[path] != n {
			t.Fatalf("sent departures = %v, want %v", stats.SentDepartures, wantSent)
		}
	}
	if len(stats.SentDepartures) != len(SentDepartures) {
		t.Fatalf("sent departures = %v, want every path present", stats.SentDepartures)
	}
	if stats.OwnOpen != 3 || stats.OwnOpenDepartures[DepartureNotInSet] != 1 || stats.OwnOpenDepartures[DepartureUntracked] != 1 ||
		len(stats.OwnOpenDepartures) != len(OwnOpenDepartures) {
		t.Fatalf("own_open %d departures %v, want 3 left, one not in set and one untracked", stats.OwnOpen, stats.OwnOpenDepartures)
	}
}

// Past the local bound, what was sent gives up its oldest and own_open
// gives up its stalest for the new: both are counted rather than read as
// departures that did not happen.
func TestTheLocalBoundIsCountedOnBothRecords(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.MaxLocalEntries = 3
	f := &setFixture{c: c, cache: mustIndex(t, options)}
	if err := f.cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	f.cache.Refresh(context.Background())
	for _, fp := range sentFingerprints(5) {
		f.send(fp)
		c.advance(time.Second)
	}
	stats := f.cache.Stats()
	if stats.SentDepartures[DepartureEvicted] != 2 || stats.Added != 3 {
		t.Fatalf("sent departures %v added %d, want 2 evicted and 3 kept", stats.SentDepartures, stats.Added)
	}
	if stats.OwnOpen != 3 || stats.OwnOpenDepartures[DepartureEvicted] != 2 {
		t.Fatalf("own_open %d departures %v, want 3 kept and 2 evicted", stats.OwnOpen, stats.OwnOpenDepartures)
	}
}
