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
)

// The gate's lookups split by whose alert they asked about. A series that
// never alerted is held by design and is not the replica's own; an alert
// this process opened and the set carries goes through; an alert this
// process opened and the set does not carry is held, counted as the
// replica's own held, and kept whole -- with the other strategy whose set
// carries the same fingerprint named, which is what a lookup keyed
// differently from the sets looks like.
func TestTheGateSaysWhichHeldLookupsWereItsOwnAlerts(t *testing.T) {
	ours := sentFingerprints(3)
	c := &clock{at: time.Unix(1700000000, 0)}
	sets := map[StrategyKey][]string{keyA: {ours[0], "theirs-1"}, keyB: {ours[1], "theirs-b"}}
	options := indexOptions(c)
	options.Policy = PolicySelfMaintain
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
	c.advance(SentConfirmAfter + time.Second)
	for _, key := range []StrategyKey{keyA, keyB} {
		f.cache.indexChanged(key)
		f.cache.RequestReconcile(key)
	}
	f.cache.Refresh(context.Background())

	if f.cache.Contains(tenant, keyA.StrategyID, "never-sent") {
		t.Fatal("a series that never alerted went through")
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, ours[0]) {
		t.Fatal("our alert the set carries was held")
	}
	if f.cache.Contains(tenant, keyA.StrategyID, ours[1]) {
		t.Fatal("our alert the set does not carry went through")
	}
	stats := f.cache.Stats()
	if stats.OwnHeld != 1 || stats.OwnLookups[AnswerIndexMember] != 1 || stats.OwnLookups[AnswerIndexAbsent] != 1 {
		t.Fatalf("own held %d own lookups %v, want one own held and one own member", stats.OwnHeld, stats.OwnLookups)
	}
	if len(stats.RecentOwnHeld) != 1 {
		t.Fatalf("recent own held = %+v, want the one", stats.RecentOwnHeld)
	}
	held := stats.RecentOwnHeld[0]
	if held.Fingerprint != ours[1] || held.StrategyID != keyA.StrategyID || held.Open || !held.Own || held.Answer != AnswerIndexAbsent ||
		len(held.InOtherSets) != 1 || held.InOtherSets[0] != keyB.StrategyID {
		t.Fatalf("held lookup = %+v, want ours[1] under %s, absent, carried by %s", held, keyA.StrategyID, keyB.StrategyID)
	}
	if len(stats.RecentLookups) != 3 || stats.RecentLookups[0].Own || stats.RecentLookups[0].Open {
		t.Fatalf("recent lookups = %+v, want all three, the first neither own nor open", stats.RecentLookups)
	}
	if stats.GateSince.IsZero() {
		t.Fatal("the own split does not say since when")
	}
	// A held lookup that was not ours has its other sets worked out when
	// read, not on the gate's path.
	f.cache.Contains(tenant, keyA.StrategyID, "theirs-b")
	stats = f.cache.Stats()
	last := stats.RecentLookups[len(stats.RecentLookups)-1]
	if last.Fingerprint != "theirs-b" || last.Own || len(last.InOtherSets) != 1 || last.InOtherSets[0] != keyB.StrategyID {
		t.Fatalf("last lookup = %+v, want theirs-b, not ours, carried by %s", last, keyB.StrategyID)
	}
	if stats.OwnHeld != 1 {
		t.Fatalf("own held %d after a lookup that was not ours", stats.OwnHeld)
	}
	// The samples are bounded.
	for i := 0; i < RecentGateLookups+5; i++ {
		c.advance(time.Second)
		f.cache.Contains(tenant, keyA.StrategyID, ours[1])
	}
	stats = f.cache.Stats()
	if len(stats.RecentLookups) != RecentGateLookups || len(stats.RecentOwnHeld) != RecentGateLookups || stats.OwnHeld != uint64(RecentGateLookups+6) {
		t.Fatalf("recent %d own held %d count %d, want bounded samples and every held counted", len(stats.RecentLookups), len(stats.RecentOwnHeld), stats.OwnHeld)
	}
	// Oldest first across the wrap.
	for i := 1; i < len(stats.RecentOwnHeld); i++ {
		if stats.RecentOwnHeld[i].At.Before(stats.RecentOwnHeld[i-1].At) {
			t.Fatalf("kept lookups out of order at %d", i)
		}
	}
}

// Both copies say since when the own split counts: it starts with the
// process, whichever copy the deployment runs.
func TestBothCopiesSaySinceWhenTheOwnSplitCounts(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	plain, err := New(Options{Source: &fakeSource{}, Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Stats().GateSince; !got.Equal(c.at) {
		t.Fatalf("plain copy gate since %v, want its start %v", got, c.at)
	}
	indexed := mustIndex(t, indexOptions(c))
	if got := indexed.Stats().GateSince; !got.Equal(c.at) {
		t.Fatalf("index copy gate since %v, want its start %v", got, c.at)
	}
}
