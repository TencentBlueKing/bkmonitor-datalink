// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
	"github.com/go-redis/redis/v8"
)

// The two Python caches, as the worker's legacy provider reads them.
type legacyCacheRedis struct {
	redis.Cmdable
	business, calendar string
	calls              int
}

func (r *legacyCacheRedis) HGet(_ context.Context, key, field string) *redis.StringCmd {
	r.calls++
	if key != "monitor.cache.cmdb.business" || field != "2" {
		return redis.NewStringResult("", fmt.Errorf("unexpected business key %s %s", key, field))
	}
	return redis.NewStringResult(r.business, nil)
}
func (r *legacyCacheRedis) GetRange(_ context.Context, key string, _, _ int64) *redis.StringCmd {
	r.calls++
	if key != "monitor.cache.calendar.7" {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(r.calendar, nil)
}

// A Plan on a Python calendar, with no frozen snapshot, is judged from the
// cache Python writes: the Slot's facts come out ACTIVE or INACTIVE, never
// UNKNOWN, for a Slot in the current minute, in the previous minute, and an
// hour back - and the first Slot after a takeover reads what it lacks itself
// rather than freezing until a background tick reaches the Plan. Three of
// those were UNKNOWN before, each freezing every Level of the Plan for the
// Slot; Python has no such answer.
func TestASlotOnAPythonCalendarIsJudgedNotFrozen(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 30, 0, time.UTC)
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "10"}
	compiled := compileEffectiveTimePlanForTest(t, identity, []uint32{5}, true, func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[{"start":"09:00","end":"17:00"}],"active_calendars":[7],"calendars":[]}}`)
	})
	source := &legacyCacheRedis{business: `{"bk_tenant_id":"tenant","time_zone":"UTC"}`,
		calendar: `[{"bk_tenant_id":"tenant","today":1649833300,"list":[{"id":2,"name":"holiday","start_time":1649833200,"end_time":1649833800,"calendar_id":7,"repeat":{},"parent_id":null,"is_first":true}]}]`}
	legacy := strategycache.NewLegacyEffectiveTime(source, source, "monitor", func() time.Time { return now }, 64, 64<<10)
	consumer := execution.ConsumerRef{Plan: identity, LevelID: 5, HasLevel: true}
	for _, tt := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"first Slot after takeover, current minute", now, strategy.EffectiveTimeActive},
		{"previous minute", now.Add(-time.Minute), strategy.EffectiveTimeActive},
		{"an hour back", now.Add(-time.Hour), strategy.EffectiveTimeActive},
		{"a second outside the time range", now.Add(-5 * time.Hour), strategy.EffectiveTimeInactive},
	} {
		header := execution.InternalExecutionHeader{
			Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: execution.EvaluationTime(tt.at.Unix())}},
			DuePlans: []execution.DuePlan{{Identity: identity, CompiledPlan: compiled}},
		}
		facts, err := PrepareEffectiveTimeFacts(context.Background(), header, legacy.Provider())
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := facts[consumer].Status(); got != tt.want {
			t.Fatalf("%s: the Level's fact is %s, want %s", tt.name, got, tt.want)
		}
	}
	if source.calls != 2 {
		t.Fatalf("four Slots within the minute read the caches %d times, want the business and the calendar once each", source.calls)
	}
	// The same calendar with no occurrence: an alerting calendar not hit
	// makes the Level inactive, as Python's in_alarm_time decides, not
	// unknown.
	source.calendar = `[]`
	empty := strategycache.NewLegacyEffectiveTime(source, source, "monitor", func() time.Time { return now }, 64, 64<<10)
	header := execution.InternalExecutionHeader{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: execution.EvaluationTime(now.Unix())}},
		DuePlans: []execution.DuePlan{{Identity: identity, CompiledPlan: compiled}},
	}
	facts, err := PrepareEffectiveTimeFacts(context.Background(), header, empty.Provider())
	if err != nil {
		t.Fatal(err)
	}
	if got := facts[consumer].Status(); got != strategy.EffectiveTimeInactive {
		t.Fatalf("an alerting calendar with no occurrence gives %s, want INACTIVE", got)
	}
}

// The evaluation line names what each Level concluded and why: the line's
// own reason is the Plan's fold, one word for the worst Level, so a Level
// suppressed by its effective time or held on a warming window had no name
// anywhere on the line. Counted per outcome, and for the outcomes that carry
// a reason, per reason.
func TestTheEvaluationLineNamesWhatEachLevelConcluded(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "10"}
	due := execution.DuePlan{Identity: identity}
	evaluated := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{
		Plan: identity,
		LevelOutcomes: []execution.LevelOutcome{
			{Plan: identity, LevelID: 1, Outcome: execution.LevelOutcomeUnknown, ReasonCode: execution.ReasonCode(contract.ReasonEffectiveTimeInactive)},
			{Plan: identity, LevelID: 2, Outcome: execution.LevelOutcomeUnknown, ReasonCode: execution.ReasonCode(contract.ReasonEffectiveTimeInactive)},
			{Plan: identity, LevelID: 3, Outcome: execution.LevelOutcomeUnknown, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming)},
			{Plan: identity, LevelID: 4, Outcome: execution.LevelOutcomeNormal},
		},
	}}}
	facts := levelOutcomeFacts(due, evaluated)
	want := []observability.LevelOutcomeFact{
		{Outcome: "NORMAL", Count: 1},
		{Outcome: "UNKNOWN", Reason: contract.ReasonEffectiveTimeInactive, Count: 2},
		{Outcome: "UNKNOWN", Reason: contract.ReasonHistoryWarming, Count: 1},
	}
	if !reflect.DeepEqual(facts, want) {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
	// A result for another Plan is not this line's to describe.
	if other := levelOutcomeFacts(execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "11"}}, evaluated); other != nil {
		t.Fatalf("facts for another Plan = %+v", other)
	}
}
