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
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

var (
	// ourMember is an alert this deployment opened; foreignMember is another
	// producer's alert in the same index set, or a member the link keeps in
	// the set and no longer holds open (redis_only).
	ourMember     = strings.Repeat("b", 32)
	foreignMember = strings.Repeat("c", 32)
)

func sentCloses(writer *maintenanceTestWriter) []linkdoutput.CloseRequest {
	var all []linkdoutput.CloseRequest
	for _, batch := range writer.batches {
		all = append(all, batch...)
	}
	return all
}

// Outside a Plan's effective time, with the link's reconcile failing for the
// strategy and its index set read fresh, each member is closed by its
// fingerprint, which stands in for the alert id: on the strategy's
// business and current revision, on this deployment's source (the writer's
// own; a close carries no other). The foreign or redis_only member is sent
// too, and that is safe: the link resolves a close to the active alert by
// tenant, this deployment's event source and fingerprint, so a member that
// is not this deployment's open alert matches nothing there. A member that
// is not a native fingerprint is not addressable and is left out without
// failing the batch. The closes are counted among close_acked as
// index_set_close_acked, and the Plan is not counted evidence_unavailable.
func TestAnInactivePlanWhoseReconcileFailsClosesItsFreshIndexSetMembers(t *testing.T) {
	f := newMaintenanceTestFixtureFrom(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceTestIndex{trusted: true,
		reconcileErr: errors.New("reconcile: link answered 503"), members: []string{ourMember, foreignMember, "not-a-fingerprint"}})
	f.m.step(context.Background())
	sent := sentCloses(f.writer)
	if len(sent) != 2 {
		t.Fatalf("sent %+v, want one close per native member", sent)
	}
	got := map[string]bool{}
	for _, r := range sent {
		if r.AlertInstanceID != r.Fingerprint || r.TenantID != "tenant-a" || r.StrategyID != 123 || r.StrategyRevision != 4 || r.BusinessID != 2 ||
			!r.OccurredAt.Equal(maintenanceTime(8, 0)) {
			t.Fatalf("close %+v, want the fingerprint as the alert id, on the Plan's business and current revision", r)
		}
		if _, err := linkdoutput.ConvertClose(r); err != nil {
			t.Fatalf("close %+v does not convert: %v", r, err)
		}
		got[r.Fingerprint] = true
	}
	if !got[ourMember] || !got[foreignMember] {
		t.Fatalf("closed %v, want both members", got)
	}
	if f.m.counts[closeOutcomeAcked] != 2 || f.m.counts[closeOutcomeIndexSetAcked] != 2 || f.m.counts[closeOutcomeEvidenceUnavailable] != 0 {
		t.Fatalf("counts %v, want two acked, both from the index set, and no evidence_unavailable", f.m.counts)
	}
	if kept := f.m.Reading().Recent[closeOutcomeIndexSetAcked]; len(kept) != 1 || kept[0].StrategyID != "123" {
		t.Fatalf("maintenance.get keeps %+v, want the strategy closed from its index set", kept)
	}
}

// What must still refuse: a set that was not read stands in for nothing
// (evidence_unavailable, no close); a global business Plan's members name no
// business and are counted close_business_unknown, one per member, and not
// sent; a reconcile that gave rows closes the alerts it listed by their own
// ids, and the members it did not list are not closed from the set.
func TestAnInactivePlanClosesFromItsIndexSetOnlyWhereThatIsAllThereIs(t *testing.T) {
	failing := errors.New("reconcile: link answered 503")
	alert := openalerts.Alert{AlertID: "instance", EventSourceID: "native", Severity: "critical", Fingerprint: ourMember}
	for _, c := range []struct {
		name    string
		source  maintenanceTestIndex
		global  bool
		sent    []string
		counted map[string]uint64
	}{
		{name: "the set never read", source: maintenanceTestIndex{trusted: true, reconcileErr: failing, readErr: errors.New("read: connection refused")},
			counted: map[string]uint64{closeOutcomeEvidenceUnavailable: 1}},
		{name: "a global business Plan", global: true, source: maintenanceTestIndex{trusted: true, reconcileErr: failing, members: []string{ourMember, foreignMember}},
			counted: map[string]uint64{closeOutcomeBusinessUnknown: 2}},
		{name: "a reconcile with rows", source: maintenanceTestIndex{trusted: true, alerts: []openalerts.Alert{alert}, members: []string{ourMember, foreignMember}},
			sent: []string{"instance"}, counted: map[string]uint64{closeOutcomeAcked: 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mutate []func(*contract.EvaluationPlanV2)
			if c.global {
				mutate = append(mutate, func(plan *contract.EvaluationPlanV2) { plan.GlobalBusiness = true })
			}
			f := newMaintenanceTestFixtureFrom(t, maintenanceReadySnapshot, maintenanceTime(8, 0), c.source, mutate...)
			// A record would answer for an id equal to a member's
			// fingerprint: a member is not closed by reading it so.
			f.m.businesses = openalerts.NewAlertBusinesses((&recordsOf{byAlert: map[string]int64{ourMember: 5, foreignMember: 5}}).read)
			f.m.step(context.Background())
			sent := sentCloses(f.writer)
			if len(sent) != len(c.sent) {
				t.Fatalf("sent %+v, want %v", sent, c.sent)
			}
			for i, r := range sent {
				if r.AlertInstanceID != c.sent[i] {
					t.Fatalf("sent %+v, want %v", sent, c.sent)
				}
			}
			for _, word := range []string{closeOutcomeAcked, closeOutcomeIndexSetAcked, closeOutcomeEvidenceUnavailable, closeOutcomeBusinessUnknown} {
				if f.m.counts[word] != c.counted[word] {
					t.Fatalf("%s counted %d, want %d: counts %v", word, f.m.counts[word], c.counted[word], f.m.counts)
				}
			}
		})
	}
}
