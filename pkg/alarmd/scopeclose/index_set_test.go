// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scopeclose

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

// indexedSet is a copy whose set cannot be judged - the link's reconcile
// gave no rows - and whose index set, read fresh, carries the fingerprints.
func indexedSet(fingerprints ...string) *fakeSet {
	set := &fakeSet{own: ownSrc, unjudged: true, held: map[string]bool{}, indexed: map[string]bool{}}
	for _, f := range fingerprints {
		set.indexed[f] = true
	}
	return set
}

// Where the reconcile gave no rows, the per-fingerprint decision is the same
// - two Slots, fresh, a member - with the index set answering membership,
// and the close is addressed by the fingerprint, which stands in for the
// alert id. A member that is another producer's alert or no open alert at
// all is closed the same way, and that is safe: the link resolves the close
// by tenant, this deployment's event source and fingerprint, and matches
// nothing for it. A fingerprint the index set does not carry stays
// unjudged, never "not a member"; a set not read fresh stands in for
// nothing; a global business strategy's close needs the alert's record,
// which a fingerprint cannot be read by, and is counted business_unknown;
// a reconcile that listed the alert closes it by its own id, as before.
func TestTheScopeCloseAddressesFromTheIndexSetOnlyWhereTheReconcileGaveNoRows(t *testing.T) {
	member, other := fp(1), fp(2)
	cases := []struct {
		name    string
		set     *fakeSet
		drops   []Drop
		want    map[string]uint64
		alertID string
	}{
		{name: "a fresh index set's member is closed by its fingerprint", set: indexedSet(member),
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeUnconfirmed: 1, OutcomeCloseSent: 1, OutcomeIndexSetCloseSent: 1}, alertID: member},
		{name: "a fingerprint the index set does not carry stays unjudged", set: indexedSet(other),
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeSetUnavailable: 2}},
		{name: "an empty index set is not judged empty", set: indexedSet(),
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeSetUnavailable: 2}},
		{name: "a set not read fresh stands in for nothing", set: func() *fakeSet { s := indexedSet(member); s.indexed = nil; return s }(),
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeSetUnavailable: 2}},
		{name: "a global business strategy's member is not closed", set: indexedSet(member),
			drops: []Drop{globalDrop(member)}, want: map[string]uint64{OutcomeUnconfirmed: 1, OutcomeBusinessUnknown: 1}},
		{name: "a listed alert is closed by its own id", set: func() *fakeSet { s := openSet(ownSrc, member); s.indexed = map[string]bool{member: true}; return s }(),
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeUnconfirmed: 1, OutcomeCloseSent: 1}, alertID: "alert-0"},
	}
	for _, c := range cases {
		f := newFixture(c.set, nil)
		f.closer.BindAlertBusiness((&alertBusinesses{byAlert: map[string]int64{member: 3, "alert-0": 3}}).of)
		f.slot(1700000000, c.drops...)
		f.slot(1700000060, c.drops...)
		f.want(t, c.name, c.want)
		sent := f.writer.sent()
		if c.alertID == "" {
			if len(sent) != 0 {
				t.Errorf("%s: sent %+v, want nothing", c.name, sent)
			}
			continue
		}
		if len(sent) != 1 || sent[0].AlertInstanceID != c.alertID || sent[0].Fingerprint != member || sent[0].BusinessID != 2 ||
			sent[0].StrategyRevision != 7 || sent[0].TenantID != tenant {
			t.Errorf("%s: sent %+v, want one close of %s by alert id %s", c.name, sent, member, c.alertID)
			continue
		}
		if _, err := linkdoutput.ConvertClose(sent[0]); err != nil {
			t.Errorf("%s: the close does not convert: %v", c.name, err)
		}
	}
}

// The index set is asked again at the step: a member that left the set
// after its two observations is not closed from it, and is counted as a
// set that cannot say, not as "not a member".
func TestAMemberThatLeftTheIndexSetBeforeTheStepIsNotClosed(t *testing.T) {
	member := fp(1)
	set := indexedSet(member)
	f := newFixture(set, nil)
	if screen := f.closer.Screen(key); screen != "" {
		t.Fatalf("Screen = %q, want the strategy judged per record", screen)
	}
	f.closer.Observe(drop(member, 1700000000))
	f.closer.Observe(drop(member, 1700000060))
	delete(set.indexed, member)
	f.closer.Step(context.Background())
	f.want(t, "left the set", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeSetUnavailable: 1})
	if sent := f.writer.sent(); len(sent) != 0 {
		t.Fatalf("sent %+v, want nothing", sent)
	}
}
