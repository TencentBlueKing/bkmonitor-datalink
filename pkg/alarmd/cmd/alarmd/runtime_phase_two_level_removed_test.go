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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// installLevels writes the strategy at revision with one Threshold Level per
// threshold - Level i+1 at thresholds[i], each triggered by one point - over
// the host, the grouping the query service answers.
func (fixture *closePathsFixture) installLevels(revision int, thresholds ...int) {
	t := fixture.t
	t.Helper()
	fixture.install(revision, []string{"bk_target_ip", "bk_target_cloud_id"}, false, nil)
	ctx := context.Background()
	key := "alarm-config.strategy_" + lifecycleStrategyID
	raw, err := fixture.life.redis.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	item := document["items"].([]any)[0].(map[string]any)
	algorithm, err := json.Marshal(item["algorithms"].([]any)[0])
	if err != nil {
		t.Fatal(err)
	}
	detect, err := json.Marshal(document["detects"].([]any)[0])
	if err != nil {
		t.Fatal(err)
	}
	var algorithms, detects []any
	for i, threshold := range thresholds {
		var levelAlgorithm, levelDetect map[string]any
		if err := json.Unmarshal(algorithm, &levelAlgorithm); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(detect, &levelDetect); err != nil {
			t.Fatal(err)
		}
		levelAlgorithm["level"], levelDetect["level"] = i+1, i+1
		levelAlgorithm["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": threshold}}}
		algorithms, detects = append(algorithms, levelAlgorithm), append(detects, levelDetect)
	}
	item["algorithms"], document["detects"] = algorithms, detects
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.life.redis.Set(ctx, key, encoded, 0).Err(); err != nil {
		t.Fatal(err)
	}
}

// publishLevels installs the strategy with installLevels and waits for the
// Leader's publication to carry its revision, and answers its facts. It
// does not wait on the Worker's view as publish does: with a Slot in flight
// the view does not settle within settleExecutableView's bound, and the
// Slot is run on unsettled either way, as the cutover cases run theirs.
func (fixture *closePathsFixture) publishLevels(revision int, thresholds ...int) controlplane.StrategyPlanFacts {
	t := fixture.t
	t.Helper()
	fixture.installLevels(revision, thresholds...)
	ctx := context.Background()
	var facts controlplane.StrategyPlanFacts
	testwait.Until(t, fmt.Sprintf("the publication carrying revision %d", revision), func() bool {
		if err := fixture.life.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("refresh after installing revision %d: %v", revision, err)
		}
		var available, found bool
		facts, available, found = fixture.control.StrategyPlanFacts(closePathsTenant, lifecycleStrategyID)
		return available && found && facts.Revision == int64(revision)
	})
	return facts
}

// endingAt is how the Query Group's Slot at evaluation ended, as the
// scheduler reported its completion; empty while it has not. A round
// stopped in its read (SLOT_CANCELLED) completes nothing.
func (fixture *closePathsFixture) endingAt(evaluation int64) string {
	life := fixture.life
	life.observationsMu.Lock()
	defer life.observationsMu.Unlock()
	for _, observation := range life.observations {
		if observation.Trace.QueryGroupKey == string(life.queryGroup) && observation.Stage == "slot_completed" &&
			observation.Result != "retrying" && observation.SlotCompletionKind != "" && observation.Trace.EvaluationTime == evaluation {
			return observation.SlotCompletionKind + "/" + string(observation.ReasonCode)
		}
	}
	return ""
}

// abnormalsAt is every ABNORMAL the strategy wrote for the Slot at
// evaluation.
func (fixture *closePathsFixture) abnormalsAt(evaluation int64) []contract.TriggerEventV1 {
	sink := fixture.life.sink
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var abnormals []contract.TriggerEventV1
	for _, event := range sink.written {
		if event.EvaluationTime == evaluation && event.PlanRef.StrategyID == lifecycleStrategyID && event.EventKind == contract.TriggerEventAbnormal {
			abnormals = append(abnormals, event)
		}
	}
	return abnormals
}

// linkAlertOf is the alert the link holds for an ABNORMAL: the message the
// link output writes for it (linkdoutput.Converter, as the bundle's sink
// converts), filed as the link files it - its id, tenant, severity, labels,
// dimensions and extra_data.
func linkAlertOf(t *testing.T, event contract.TriggerEventV1) *fakeLinkdAlert {
	t.Helper()
	converter, err := linkdoutput.NewConverter(nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := converter.Convert(&event)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		TenantID   string                     `json:"bk_tenant_id"`
		Dimensions map[string]json.RawMessage `json:"dimensions"`
		Labels     map[string]json.RawMessage `json:"labels"`
		ExtraData  json.RawMessage            `json:"extra_data"`
	}
	if err := json.Unmarshal(message.Payload, &wire); err != nil {
		t.Fatal(err)
	}
	alert := &fakeLinkdAlert{id: message.AlertID, tenant: wire.TenantID, severity: message.Severity, labels: map[string]string{},
		dimensions: map[string]string{}, extra: wire.ExtraData}
	for name, value := range wire.Labels {
		alert.labels[name] = fakeKeyword(value)
	}
	for name, value := range wire.Dimensions {
		alert.dimensions[name] = fakeKeyword(value)
	}
	return alert
}

// levelDeletedInFlight is the strategy at revision 1 with two Levels -
// Level 1 at 80, Level 2 at 50 - edited at revision 2 to Level 1 alone while
// the Slot of its second round is in flight: begun under revision 1's
// Segment and stopped in its read, then the edit published with the clock
// past that Slot's time, so the Slot falls in the Segment the edit closes.
// The query service answers value for that Slot, 40 before it. Its edit
// close is the production one, over the bundle's control plane and Redis -
// the record of the episodes the bundle opened - sending to link, which
// holds what a case files there. The bundle's own close loop sends to the
// fixture's stub; it ran its first round at the start, before anything
// opened, and its next is absentCloseInterval on.
type levelDeletedInFlight struct {
	*closePathsFixture
	inFlight int64
	link     *fakeLinkd
	closer   *filterclose.Closer
	edits    *editStrategyClose
	lines    *lockedBuffer
}

func deleteALevelInFlight(t *testing.T, value float64) *levelDeletedInFlight {
	t.Helper()
	fixture := startClosePathsFixtureWith(t, func(fixture *closePathsFixture) { fixture.installLevels(1, 80, 50) })
	life := fixture.life
	ctx := context.Background()
	if facts := fixture.facts(); facts.Revision != 1 || len(facts.Levels) != 2 || !facts.LevelsWhole {
		t.Fatalf("revision 1 publishes %+v, want its two Levels whole", facts)
	}
	life.serve(false, 40, lifecycleHostA)
	life.mustAttempt(1)

	deleted := &levelDeletedInFlight{closePathsFixture: fixture, inFlight: life.evaluationAt(2)}
	roundCtx, stopRound := context.WithCancel(ctx)
	life.interceptQueries(func(_ http.ResponseWriter, request *http.Request) bool {
		// Read to its end: only then does the server see the client go.
		_, _ = io.Copy(io.Discard, request.Body)
		stopRound()
		<-request.Context().Done()
		return true
	})
	life.clock.Store(roundReadAtMillis(life.cfg, life.queryGroup, deleted.inFlight, life.interval))
	waitSourceProbes(life.bundle)
	result, attempted, err := life.runner.RunOne(roundCtx)
	life.interceptQueries(nil)
	stopRound()
	if !attempted || result.Completed || !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped Slot RunOne = (%+v, %t, %v), want an attempted, uncompleted Slot stopped in its read", result, attempted, err)
	}

	if facts := fixture.publishLevels(2, 80); len(facts.Levels) != 1 || facts.Levels[0].LevelID != 1 || !facts.LevelsWhole {
		t.Fatalf("revision 2 publishes %+v, want Level 1 alone, whole", facts)
	}
	if segment := life.segmentAt(2); segment.End == nil || int64(*segment.End) <= deleted.inFlight {
		t.Fatalf("the in-flight Slot's Segment after the edit is %+v, want it closed after the Slot's time %d", segment, deleted.inFlight)
	}

	// The Slot in flight runs to its end, each round at its next ready
	// instant. It is not failed for not ending: what it wrote says more.
	life.serve(false, value, lifecycleHostA)
	for attempt := 0; attempt < 30 && fixture.endingAt(deleted.inFlight) == ""; attempt++ {
		now := time.UnixMilli(life.clock.Load())
		if next := life.runner.NextReadyAt(); next.After(now) {
			life.clock.Store(next.UnixMilli() + 1)
		}
		waitSourceProbes(life.bundle)
		if _, attempted, err := life.runner.RunOne(ctx); err != nil {
			t.Fatalf("the in-flight Slot RunOne: %v", err)
		} else if !attempted {
			life.clock.Store(life.clock.Load() + time.Second.Milliseconds())
		}
	}

	deleted.link = newFakeLinkd(t)
	server := httptest.NewServer(deleted.link)
	t.Cleanup(server.Close)
	// As the bundle wires them (runtime_phase_two_bundle.go), over the
	// bundle's Redis, sending to the link that holds the alerts.
	cfg := life.cfg
	cfg.PhaseTwo.Linkd.ControlPlane.URL = server.URL
	now := func() time.Time { return time.UnixMilli(life.clock.Load()) }
	client, err := newLinkdManage(cfg, now)
	if err != nil || client == nil {
		t.Fatalf("link client %v (%v)", client, err)
	}
	deleted.lines = &lockedBuffer{}
	deleted.closer, err = newFilterCloser(cfg, client, life.redis, now, nil, observability.New("alarmd", deleted.lines))
	if err != nil || deleted.closer == nil {
		t.Fatalf("closer %v (%v)", deleted.closer, err)
	}
	deleted.edits = newEditStrategyClose(life.bundle, fixture.control, newFilterRoute(cfg, deleted.closer))
	return deleted
}

// editRound is one round of the edit close over the strategy, which the
// roster holds.
func (deleted *levelDeletedInFlight) editRound() {
	deleted.edits.step(context.Background(), map[absentalerts.Key]struct{}{{TenantID: closePathsTenant, StrategyID: lifecycleStrategyID}: {}})
}

// levelRemovedLines is the edit close's job lines of level_removed.
func (deleted *levelDeletedInFlight) levelRemovedLines() []map[string]any {
	var lines []map[string]any
	for _, line := range deleted.lines.jobLines(deleted.t) {
		if line["decision"] == editclose.OutcomeLevelRemoved {
			lines = append(lines, line)
		}
	}
	return lines
}

// An edit deletes Level 2 while a Slot is in flight whose frozen
// configuration still has it, and Level 2's trigger holds in that Slot
// (design 013 section 3.3). The Slot completes under the configuration it
// was frozen with, so it opens one alert at Level 2 under the old
// strategy_version. The edit close's next round closes it as level_removed:
// its version is older than the Plans', and the Plans no longer detect its
// level. Its job line says so (decision level_removed, closed 1), the line
// that counts the decision's closes.
func TestAnEditDeletingALevelClosesTheAlertItsInFlightSlotOpenedAtIt(t *testing.T) {
	deleted := deleteALevelInFlight(t, 60)
	abnormals := deleted.abnormalsAt(deleted.inFlight)
	if len(abnormals) != 1 || abnormals[0].PrimaryLevelID != 2 {
		t.Fatalf("the Slot in flight at the edit wrote ABNORMALs %+v, want one alert opened at the deleted Level 2; it ended %q",
			abnormals, deleted.endingAt(deleted.inFlight))
	}
	if ended := deleted.endingAt(deleted.inFlight); ended != fullEnding {
		t.Fatalf("the in-flight Slot ended %q, want FULL under the configuration it was frozen with", ended)
	}
	alert := linkAlertOf(t, abnormals[0])
	if alert.severity != "warning" || alert.labels["strategy_version"] != "1" {
		t.Fatalf("the link holds %+v, want Level 2's alert under strategy_version 1", alert)
	}
	// The record of the episodes the bundle opened, written behind the
	// acknowledged write, is what the close's expectation counts.
	store, err := openepisodes.NewStore(deleted.life.redis, deleted.life.cfg.Redis.StatePrefix)
	if err != nil {
		t.Fatal(err)
	}
	testwait.Until(t, "the record holding the episode opened at version 1", func() bool {
		record, err := store.Load(context.Background(), openepisodes.Key{TenantID: closePathsTenant, StrategyID: lifecycleStrategyID})
		if err != nil {
			return false
		}
		for _, entry := range record.Entries {
			if entry.Version == "1" {
				return true
			}
		}
		return false
	})
	deleted.link.add(alert)

	deleted.editRound()
	if closed := deleted.link.closed(); len(closed) != 1 || closed[alert.id] != 1 {
		t.Fatalf("the edit close closed %v, want Level 2's alert %s once", closed, alert.id)
	}
	deleted.link.mu.Lock()
	reason := alert.reason
	deleted.link.mu.Unlock()
	if !strings.Contains(reason, "strategy "+lifecycleStrategyID+" changed (level_removed)") {
		t.Fatalf("the alert was closed for %q, want level_removed", reason)
	}
	// The record began with this very episode, so it does not vouch for the
	// alert yet (its start plus the settle): the level job's part since the
	// start matches nothing, and the part before it decides the alert on
	// its own record (editclose.Decide) and closes it.
	lines := deleted.levelRemovedLines()
	closed, done := 0.0, false
	for _, line := range lines {
		n, _ := line["closed"].(float64)
		closed += n
		done = done || (line["result"] == filterclose.OutcomeJobDone && n == 1)
	}
	if closed != 1 || !done {
		t.Fatalf("the level_removed job lines are %v, want a job_done that closed 1 and nothing else closed", lines)
	}
	// The bucket of version 1 is left to the level rule: Level 2 is not
	// among the Plans'.
	stats := deleted.closer.Stats()
	if stats["edit_"+editclose.OutcomeGroupLevelUndecided] != 1 || stats[filterclose.OutcomeItemClosed] != 1 {
		t.Fatalf("linkd_filter_close_total %v, want the version 1 bucket left to the level rule and one alert closed", stats)
	}
}

// The same edit with Level 2's trigger not holding in the Slot in flight:
// the Slot completes under its frozen configuration and opens nothing, and
// the edit close's next round closes nothing.
func TestAnEditDeletingALevelWhoseTriggerDoesNotHoldInFlightOpensAndClosesNothing(t *testing.T) {
	deleted := deleteALevelInFlight(t, 40)
	if abnormals := deleted.abnormalsAt(deleted.inFlight); len(abnormals) != 0 {
		t.Fatalf("the Slot in flight at the edit wrote ABNORMALs %+v below every threshold", abnormals)
	}
	if ended := deleted.endingAt(deleted.inFlight); ended != fullEnding {
		t.Fatalf("the in-flight Slot ended %q, want FULL under the configuration it was frozen with", ended)
	}
	deleted.editRound()
	if closes := deleted.link.requests("/api/v1/alerts/close"); len(closes) != 0 {
		t.Fatalf("the edit close sent closes %v with nothing opened", closes)
	}
	if lines := deleted.levelRemovedLines(); len(lines) != 0 {
		t.Fatalf("the edit close wrote level_removed job lines %v with nothing opened", lines)
	}
	if stats := deleted.closer.Stats(); stats[filterclose.OutcomeItemClosed] != 0 {
		t.Fatalf("linkd_filter_close_total %v, want nothing closed", stats)
	}
}
