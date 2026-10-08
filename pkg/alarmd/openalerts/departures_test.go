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

// Each way out of the two records is counted by its path. The one that was
// read wrongly on a live deployment is not_resent: an alert that stops being
// sent leaves comparison.sent without a RECOVERY, yet stays in own_open and
// is still asked about as the replica's own.
func TestEveryDepartureFromWhatWasSentIsCountedByItsPath(t *testing.T) {
	ours := sentFingerprints(4)
	c := &clock{at: time.Unix(1700000000, 0)}
	sets := map[StrategyKey][]string{keyA: {"theirs-1"}, keyB: {"theirs-b"}}
	options := indexOptions(c)
	options.Source = setReaderFunc(func(_ context.Context, key StrategyKey) ([]string, error) { return sets[key], nil })
	options.Reconciler = reconcilerFunc(func(_ context.Context, key StrategyKey) (Reconciliation, error) {
		return Reconciliation{Members: append([]string(nil), sets[key]...)}, nil
	})
	f := &disjointFixture{c: c, cache: mustIndex(t, options)}
	if err := f.cache.SetTracked([]StrategyKey{keyA, keyB}); err != nil {
		t.Fatal(err)
	}
	f.cache.Refresh(context.Background())
	f.send(ours...)
	f.cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyB, "ours-b")})

	stats := f.cache.Stats()
	if !stats.OwnOpenKnown || stats.OwnOpen != 5 || stats.Added != 5 {
		t.Fatalf("after five sends own_open=%d (known %t) added=%d, want 5 and 5", stats.OwnOpen, stats.OwnOpenKnown, stats.Added)
	}

	// A RECOVERY the broker took leaves both records.
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, ours[0])})

	// Three alerts no longer sent: past the retention, the next calibration
	// prunes them from what was sent. They are still open.
	f.reread(options.LocalRetention + time.Second)
	stats = f.cache.Stats()
	if stats.SentDepartures[DepartureNotResent] != 3 || stats.OwnOpen != 4 {
		t.Fatalf("after the calibration sent departures %v own_open %d, want 3 not_resent and 4 still open", stats.SentDepartures, stats.OwnOpen)
	}
	// Still the replica's own at the gate.
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
	wantSent := map[string]uint64{DepartureRecoveryAcked: 1, DepartureNotResent: 3, DepartureUntracked: 1, DepartureEvicted: 0}
	for path, n := range wantSent {
		if stats.SentDepartures[path] != n {
			t.Fatalf("sent departures = %v, want %v", stats.SentDepartures, wantSent)
		}
	}
	if len(stats.SentDepartures) != len(SentDepartures) {
		t.Fatalf("sent departures = %v, want every path present", stats.SentDepartures)
	}
	if stats.OwnOpen != 3 || stats.OwnOpenDepartures[DepartureRecoveryAcked] != 1 || stats.OwnOpenDepartures[DepartureUntracked] != 1 ||
		len(stats.OwnOpenDepartures) != len(OwnOpenDepartures) {
		t.Fatalf("own_open %d departures %v, want 3 left, one recovered and one untracked", stats.OwnOpen, stats.OwnOpenDepartures)
	}

	// A RECOVERY for an alert the records no longer hold counts nothing.
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, ours[0])})
	if again := f.cache.Stats(); again.SentDepartures[DepartureRecoveryAcked] != 1 || again.OwnOpenDepartures[DepartureRecoveryAcked] != 1 {
		t.Fatalf("a repeated RECOVERY was counted again: %v %v", again.SentDepartures, again.OwnOpenDepartures)
	}
}

// Past the local bound, what was sent gives up its oldest and own_open
// refuses the new: both are counted rather than read as departures that
// did not happen.
func TestTheLocalBoundIsCountedOnBothRecords(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.MaxLocalEntries = 3
	f := &disjointFixture{c: c, cache: mustIndex(t, options)}
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
	if stats.OwnOpen != 3 || stats.OwnOpenRefusals != 2 {
		t.Fatalf("own_open %d refused %d, want 3 kept and 2 refused", stats.OwnOpen, stats.OwnOpenRefusals)
	}
}

// Without the index protocol there is no own-open record: its fields say
// they are unknown, and the refresh's pruning is still counted.
func TestWithoutTheIndexOwnOpenIsUnknownAndPruningIsCounted(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	source := &fakeSource{publication: Publication{Heartbeat: fresh(c, time.Minute)}}
	cache := newCache(t, source, c, PolicySelfMaintain)
	cache.Track(keyA)
	cache.Refresh(context.Background())
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "f1")})
	c.advance(LocalRetentionCycles*time.Minute + time.Second)
	source.publication.Heartbeat = fresh(c, time.Minute)
	cache.Refresh(context.Background())
	stats := cache.Stats()
	if stats.OwnOpenKnown || stats.OwnOpenDepartures != nil {
		t.Fatalf("own_open claimed without the index: %+v", stats)
	}
	if stats.SentDepartures[DepartureNotResent] != 1 || len(stats.SentDepartures) != len(SentDepartures) {
		t.Fatalf("sent departures = %v, want one not_resent and every path present", stats.SentDepartures)
	}
}
