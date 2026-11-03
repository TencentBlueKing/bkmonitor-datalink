// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// A global business strategy files each alert under the business the alert
// is about, and every close of one carries that business - read from the
// alert's own record - not the strategy's. These are the three closes: of a
// strategy that no longer exists, outside a strategy's effective time, and
// of a series that left the strategy's target.

func absentBusinesses(batches [][]linkdoutput.CloseRequest) map[string]int64 {
	got := map[string]int64{}
	for _, batch := range batches {
		for _, request := range batch {
			got[request.AlertInstanceID] = request.BusinessID
		}
	}
	return got
}

func twoAbsentAlerts() []openalerts.Alert {
	return []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef"),
		nativeAlert("alert-2", "fedcba9876543210fedcba9876543210")}
}

func TestAGlobalStrategysAbsentCloseCarriesEachAlertsBusiness(t *testing.T) {
	fixture := newAbsentFixture(t, twoAbsentAlerts())
	fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 9, Revision: 7, GlobalBusiness: true}}
	for id, business := range map[string]int64{"alert-1": 2, "alert-2": 3} {
		record := fixture.link.records[id]
		record.BusinessID = business
		fixture.link.records[id] = record
	}
	fixture.mature(context.Background())
	got := absentBusinesses(fixture.writer.batches)
	if len(got) != 2 || got["alert-1"] != 2 || got["alert-2"] != 3 {
		t.Fatalf("closes carried %v, want each alert's own business (2 and 3), not the strategy's 9", got)
	}
	if fixture.link.reads != 2 {
		t.Fatalf("%d record reads, want one per alert", fixture.link.reads)
	}
}

func TestAnOrdinaryStrategysAbsentCloseReadsNoRecord(t *testing.T) {
	fixture := newAbsentFixture(t, twoAbsentAlerts())
	fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5, Revision: 7}}
	fixture.mature(context.Background())
	got := absentBusinesses(fixture.writer.batches)
	if len(got) != 2 || got["alert-1"] != 5 || got["alert-2"] != 5 || fixture.link.reads != 0 {
		t.Fatalf("closes carried %v with %d record reads, want both under the strategy's 5 and no read", got, fixture.link.reads)
	}
}

func TestAnAbsentAlertWhoseRecordNamesNoBusinessStaysOpenThisRound(t *testing.T) {
	fixture := newAbsentFixture(t, twoAbsentAlerts())
	fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 9, Revision: 7, GlobalBusiness: true}}
	delete(fixture.link.records, "alert-2")
	fixture.mature(context.Background())
	got := absentBusinesses(fixture.writer.batches)
	if len(got) != 1 || got["alert-1"] != 2 {
		t.Fatalf("closes carried %v, want only alert-1 under its record's business", got)
	}
	if fixture.loop.Stats()[absentalerts.OutcomeBusinessUnknown] != 1 {
		t.Fatalf("the alert left open was not counted: %v", fixture.loop.Stats())
	}
}

// A strategy the catalog never knew may have been a global one; nothing but
// its alerts' records says, so each close reads its own.
func TestAnUnrememberedStrategysAbsentCloseReadsEachAlertsBusiness(t *testing.T) {
	fixture := newAbsentFixture(t, twoAbsentAlerts())
	record := fixture.link.records["alert-2"]
	record.BusinessID = 3
	fixture.link.records["alert-2"] = record
	fixture.mature(context.Background())
	got := absentBusinesses(fixture.writer.batches)
	if len(got) != 2 || got["alert-1"] != 2 || got["alert-2"] != 3 {
		t.Fatalf("closes carried %v, want each alert's record's business", got)
	}
}

// A strategy the catalog knew as an ordinary one stays ordinary when only
// its revision has to be read from a record: that one read is all, and the
// closes carry the strategy's business.
func TestAKnownOrdinaryStrategyReadsOnlyItsMissingRevision(t *testing.T) {
	fixture := newAbsentFixture(t, twoAbsentAlerts())
	fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5}}
	fixture.mature(context.Background())
	got := absentBusinesses(fixture.writer.batches)
	if len(got) != 2 || got["alert-1"] != 5 || got["alert-2"] != 5 || fixture.link.reads != 1 {
		t.Fatalf("closes carried %v with %d record reads, want both under the strategy's 5 and one read for the revision", got, fixture.link.reads)
	}
}

func maintenanceAlertWithID(id, fingerprint string) openalerts.Alert {
	return openalerts.Alert{AlertID: id, EventSourceID: "native", Severity: "critical", Fingerprint: fingerprint}
}

func maintenanceBusinesses(batches [][]linkdoutput.CloseRequest) map[string]int64 {
	return absentBusinesses(batches)
}

// recordsOf is the link's record of each alert for the maintenance close,
// counting the reads.
type recordsOf struct {
	byAlert map[string]int64
	reads   int
}

func (r *recordsOf) read(_ context.Context, _, alertID string) (openalerts.AlertRecord, error) {
	r.reads++
	business, ok := r.byAlert[alertID]
	if !ok {
		return openalerts.AlertRecord{}, openalerts.ErrAlertNotFound
	}
	return openalerts.AlertRecord{AlertID: alertID, EventSourceID: "native", StrategyID: "123", BusinessID: business}, nil
}

func twoMaintenanceAlerts() []openalerts.Alert {
	return []openalerts.Alert{maintenanceAlertWithID("alert-1", "11111111111111111111111111111111"),
		maintenanceAlertWithID("alert-2", "22222222222222222222222222222222")}
}

func TestAGlobalPlansOffHoursCloseCarriesEachAlertsBusiness(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), twoMaintenanceAlerts(),
		func(plan *contract.EvaluationPlanV2) { plan.GlobalBusiness = true })
	records := &recordsOf{byAlert: map[string]int64{"alert-1": 5}}
	f.m.businesses = openalerts.NewAlertBusinesses(records.read)
	f.m.step(context.Background())
	got := maintenanceBusinesses(f.writer.batches)
	if len(got) != 1 || got["alert-1"] != 5 {
		t.Fatalf("closes carried %v, want alert-1 under its record's 5 and alert-2 not sent", got)
	}
	if f.m.Stats()[closeOutcomeBusinessUnknown] != 1 {
		t.Fatalf("the alert left open was not counted: %v", f.m.Stats())
	}
}

func TestAnOrdinaryPlansOffHoursCloseReadsNoRecord(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), twoMaintenanceAlerts())
	records := &recordsOf{byAlert: map[string]int64{"alert-1": 5, "alert-2": 6}}
	f.m.businesses = openalerts.NewAlertBusinesses(records.read)
	f.m.step(context.Background())
	got := maintenanceBusinesses(f.writer.batches)
	if len(got) != 2 || got["alert-1"] != 2 || got["alert-2"] != 2 || records.reads != 0 {
		t.Fatalf("closes carried %v with %d record reads, want both under the Plan's 2 and no read", got, records.reads)
	}
}

// The access path's drop reaches the close with its Plan's kind.
func TestTheScopeDropSinkCarriesTheGlobalBusinessFlag(t *testing.T) {
	closer := scopeclose.New(scopeclose.Options{MaxEntries: 4, Batch: 4, ObservationTTL: time.Hour, Freshness: time.Hour})
	set := &globalTestSet{alerts: []openalerts.Alert{{AlertID: "alert-1", EventSourceID: "native", Fingerprint: "11111111111111111111111111111111"}}}
	writer := &absentTestWriter{}
	closer.Bind(set, writer)
	records := &recordsOf{byAlert: map[string]int64{"alert-1": 5}}
	businesses := openalerts.NewAlertBusinesses(records.read)
	closer.BindAlertBusiness(businesses.Of)
	sink := scopeDropSink{closer: closer}
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "9", StrategyID: "123"}
	for round := int64(1700000000); round <= 1700000060; round += 60 {
		sink.Observe(access.ScopeDrop{Plan: plan, Fingerprint: "11111111111111111111111111111111", StrategyRevision: 4,
			GlobalBusiness: true, Round: round, IntervalSeconds: 60})
	}
	closer.Step(context.Background())
	got := absentBusinesses(writer.batches)
	if len(got) != 1 || got["alert-1"] != 5 {
		t.Fatalf("closes carried %v, want alert-1 under its record's 5", got)
	}
}

// globalTestSet is a trusted open set of one strategy holding its alerts.
type globalTestSet struct{ alerts []openalerts.Alert }

func (set *globalTestSet) Trusted() bool { return true }
func (set *globalTestSet) MemberCount(openalerts.StrategyKey) (int, bool) {
	return len(set.alerts), true
}
func (set *globalTestSet) Holds(_ openalerts.StrategyKey, fingerprint string) (bool, bool) {
	for _, alert := range set.alerts {
		if alert.Fingerprint == fingerprint {
			return true, true
		}
	}
	return false, true
}
func (set *globalTestSet) ActiveAlerts(openalerts.StrategyKey) []openalerts.Alert { return set.alerts }
func (set *globalTestSet) OwnEventSourceID() string                               { return "native" }
func (set *globalTestSet) UnlistedMemberCount(openalerts.StrategyKey) (int, bool) { return 0, false }
func (set *globalTestSet) UnlistedHolds(openalerts.StrategyKey, string) (bool, bool) {
	return false, false
}
