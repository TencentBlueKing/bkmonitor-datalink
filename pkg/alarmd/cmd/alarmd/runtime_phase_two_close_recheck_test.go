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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// addMaintenancePlan compiles one more Plan into the fixture's Query Group,
// active in its own time range, and tracks its alerts in the same open-alert
// copy, so a step judges two Plans of one group.
func addMaintenancePlan(t *testing.T, f *maintenanceTestFixture, strategyID int64, snapshot, start string) {
	t.Helper()
	catalog := productionG4Catalog(t, strategyID, "Threshold", "usage", "system.cpu", []string{"host"}, [][]map[string]any{{{"method": "gte", "threshold": 50}}})
	group := catalog.QueryGroups[0]
	p := group.Plans[0]
	p.Plan.WireFormat = contract.WireFormatStandardRawEvent
	p.Plan.LegacyOutput = nil
	p.Plan.StrategyRef.SnapshotRevision = 4
	p.Plan.StrategyIR.StrategyRef.SnapshotRevision = 4
	p.Plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[{"start":"` +
		start + `","end":"17:00"}],"active_calendars":[],"calendars":[]}}`)
	p.Plan.EffectiveTimeSnapshot = json.RawMessage(snapshot)
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), config.Default().CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: p.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "state-v1", CodecSemanticsVersion: "codec-v1", IdentitySchemaDigest: strings.Repeat("c", 64), SourceTimeSemanticsVersion: "seconds-v1", HistoryCellSemanticsVersion: "history-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compile terminal=%+v", result.PlanTerminal())
	}
	key := openalerts.StrategyKey{TenantID: p.Identity.TenantID, StrategyID: p.Identity.StrategyID}
	if err := f.m.cache.TrackOwned(key); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !f.m.cache.Snapshot(key).Calibrated {
		if time.Now().After(deadline) {
			t.Fatal("cache did not calibrate the second Plan")
		}
		time.Sleep(time.Millisecond)
	}
	catalogRead := f.m.catalog.(*maintenanceTestCatalog)
	catalogRead.plans = append(catalogRead.plans, controlplane.MaintenancePlan{Identity: p.Identity, Compiled: compiled})
}

// flakyLegacyProvider answers the first resolution as a static schedule
// does and fails every one after it: a time query that works at the first
// look and not at the recheck.
type flakyLegacyProvider struct{ calls int }

func (p *flakyLegacyProvider) Resolve(ctx context.Context, requests []strategy.EffectiveTimeRequest) ([]strategy.EffectiveTimeFact, error) {
	p.calls++
	if p.calls > 1 {
		return nil, errors.New("calendar service did not answer")
	}
	return strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return time.UTC, nil
	})).Resolve(ctx, requests)
}

// The recheck before a close send is about the one Plan it rechecks: its
// effective time is the Plan's own. A Plan whose boundary turned active, or
// whose time could not be read, between the first look and the send is
// counted under its own word and not sent, and the step goes on to the
// group's next Plan, which is sent this round; the group's Plans are not
// read again for it, since nothing about the lease moved.
func TestARecheckRefusingOnePlanCountsItAndClosesTheGroupsOtherPlans(t *testing.T) {
	for _, c := range []struct {
		name, snapshot, word string
		flaky                bool
	}{
		{name: "the boundary turned active", snapshot: maintenanceReadySnapshot, word: closeOutcomeBoundaryActive},
		{name: "the time could not be read", snapshot: "", word: closeOutcomeEffectiveTimeUnknown, flaky: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The first Plan is active from 09:00; the second from 10:00.
			f := newMaintenanceTestFixture(t, c.snapshot, maintenanceTime(8, 59), []openalerts.Alert{maintenanceAlert("native", "critical")})
			if c.flaky {
				f.m.legacy = &flakyLegacyProvider{}
			}
			addMaintenancePlan(t, f, 124, maintenanceReadySnapshot, "10:00")
			rechecks := 0
			f.runner.check = func(context.Context) error {
				rechecks++
				if rechecks == 1 && !c.flaky {
					// The first Plan's send: its boundary passes meanwhile.
					f.advance(time.Minute)
				}
				return nil
			}
			f.m.step(context.Background())
			if len(f.writer.batches) != 1 || len(f.writer.batches[0]) != 1 || f.writer.batches[0][0].StrategyID != 124 {
				t.Fatalf("sent %+v, want one batch: the second Plan's alert", f.writer.batches)
			}
			if got := f.m.counts[c.word]; got != 1 {
				t.Fatalf("%s counted %d times, want once: counts %v", c.word, got, f.m.counts)
			}
			for qg := range f.m.bundle.runners {
				if f.m.groups[qg] == nil {
					t.Fatal("the group's Plans were dropped for a refusal that has nothing to do with the lease")
				}
			}
		})
	}
}

// A recheck whose time query fails outright - here the step's context
// ending between the owner check and the recheck - is the same unknown
// time, counted once under its word and not sent; the step then stops on
// its ended context rather than going on to the next Plan.
func TestARecheckWhoseTimeQueryFailsCountsItsTimeUnknown(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 59), []openalerts.Alert{maintenanceAlert("native", "critical")})
	addMaintenancePlan(t, f, 124, maintenanceReadySnapshot, "10:00")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.runner.check = func(context.Context) error {
		cancel()
		return nil
	}
	f.m.step(ctx)
	if len(f.writer.batches) != 0 {
		t.Fatalf("sent %+v, want nothing", f.writer.batches)
	}
	if got := f.m.counts[closeOutcomeEffectiveTimeUnknown]; got != 1 {
		t.Fatalf("effective_time_unknown counted %d times, want once: counts %v", got, f.m.counts)
	}
}
