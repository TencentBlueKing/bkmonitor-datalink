package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// alertDaysOn makes the strategy alert only on the days the calendar lists,
// all day long: whether it is effective is the calendar's answer alone.
func alertDaysOn(calendar int64) func(*contract.EvaluationPlanV2) {
	return func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(fmt.Sprintf(`{"window_size":1,"required_anomalies":1,"step_seconds":60,`+
			`"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[{"start":"00:00","end":"23:59"}],"active_calendars":[%d],"calendars":[]}}`, calendar))
	}
}

// alertDaysOnBesideRestDays is alertDaysOn(active) with rest days on another
// calendar, present throughout, so that the active one reading deleted is
// never every calendar the strategies name.
func alertDaysOnBesideRestDays(active, rest int64) func(*contract.EvaluationPlanV2) {
	return func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(fmt.Sprintf(`{"window_size":1,"required_anomalies":1,"step_seconds":60,`+
			`"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[{"start":"00:00","end":"23:59"}],"active_calendars":[%d],"calendars":[%d]}}`, active, rest))
	}
}

// A calendar item that does not cover the fixture's moment.
const calendarItemElsewhere = `{"id":1,"time_kind":"UNIX_SECONDS","start_time":100,"end_time":200,"time_zone":"UTC","parent_id":null,"repeat":{}}`

// calendarItemCovering is a calendar item that covers the fixture's moment
// and the hour either side of it.
func calendarItemCovering(at time.Time) string {
	return fmt.Sprintf(`{"id":1,"time_kind":"UNIX_SECONDS","start_time":%d,"end_time":%d,"time_zone":"UTC","parent_id":null,"repeat":{}}`,
		at.Add(-time.Hour).Unix(), at.Add(time.Hour).Unix())
}

func deletedCalendar(id int64) string {
	return fmt.Sprintf(`{"id":%d,"bk_tenant_id":"tenant-a","status":"DELETED","items":[]}`, id)
}

func presentCalendar(id int64, items ...string) string {
	return fmt.Sprintf(`{"id":%d,"bk_tenant_id":"tenant-a","status":"PRESENT","items":[%s]}`, id, strings.Join(items, ","))
}

func calendarSnapshot(calendars ...string) string {
	return `{"schema_version":1,"status":"READY","business_timezone":"UTC","calendars":[` + strings.Join(calendars, ",") + `]}`
}

// compiledMaintenancePlans is the fixture strategy compiled against the
// snapshot, for a catalog to answer with.
func compiledMaintenancePlans(t *testing.T, snapshot string, at time.Time, mutate ...func(*contract.EvaluationPlanV2)) []controlplane.MaintenancePlan {
	t.Helper()
	return newMaintenanceTestFixture(t, snapshot, at, nil, mutate...).m.catalog.(*maintenanceTestCatalog).plans
}

// twoGroups puts the fixture's maintenance over two Query Groups, qg-a and
// qg-b, each answered by its own catalog.
func twoGroups(f *maintenanceTestFixture, a, b []controlplane.MaintenancePlan) (maintenanceCatalogByGroup, *maintenanceTestRunner, *maintenanceTestRunner) {
	first := &maintenanceTestRunner{check: func(context.Context) error { return nil }, scope: "obj", revision: 1}
	second := &maintenanceTestRunner{check: func(context.Context) error { return nil }, scope: "obj", revision: 1}
	f.m.bundle.runners = map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{"qg-a": {runner: first}, "qg-b": {runner: second}}
	catalogs := maintenanceCatalogByGroup{"qg-a": {plans: a}, "qg-b": {plans: b}}
	f.m.catalog = catalogs
	return catalogs, first, second
}

// Every calendar the strategies name reads deleted at once: the writer's
// calendar source answered nothing, and each calendar it could not find
// arrived marked deleted. The strategy that alerts only on calendar days is
// inactive, as Python reads it - detection is unchanged - and its alerts
// are not closed for it, however long that lasts; each held close is
// counted, and logged as degraded with the strategy and the calendar.
func TestEveryCalendarReadingDeletedAtOnceClosesNothing(t *testing.T) {
	f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7)), maintenanceTime(8, 0),
		[]openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOn(7))
	plan := f.m.catalog.(*maintenanceTestCatalog).plans[0]
	fact, err := plan.Compiled.ResolveEffectiveTime(context.Background(), maintenanceTime(8, 0).Unix())
	if err != nil || fact.Status() != strategy.EffectiveTimeInactive {
		t.Fatalf("detection reads the effective time as (%v, %v), want inactive as before", fact.Status(), err)
	}
	var observed capturedObservations
	f.m.bundle.dependencies.Observer = &observed

	f.m.step(context.Background())
	f.advance(2 * calendarDeletionSettle)
	f.m.step(context.Background())

	if f.runner.lastErr != nil || len(f.writer.batches) != 0 {
		t.Fatalf("err=%v batches=%v, want no close while every calendar reads deleted", f.runner.lastErr, f.writer.batches)
	}
	if held := f.m.Stats()[closeOutcomeDeletionUnsettled]; held != 2 {
		t.Fatalf("held closes counted %d, want one a step", held)
	}
	var line *observability.Observation
	for i := range observed {
		if observed[i].ReasonCode == observability.EffectiveCloseCalendarDeletionUnsettled {
			line = &observed[i]
		}
	}
	if line == nil || line.Result != observability.ResultDegraded || line.Trace.StrategyID != plan.Identity.StrategyID ||
		!errors.Is(line.Err, errCalendarDeletionUnsettled) || !strings.Contains(line.Err.Error(), "calendar 7") {
		t.Fatalf("held close observed as %+v, want a degraded line naming the strategy and calendar 7", line)
	}
}

// One calendar deleted among ones still present is a deletion, and the
// strategy that named it as its only alert days is closed, as Python closes
// it - once it has read deleted for the settle time. Before that it is what
// a calendar source lost in the middle of a writer round looks like: the
// pages written before the loss hold their calendars present, the ones
// after it deleted, in one publication.
func TestACalendarDeletedAmongPresentOnesClosesOnceTheDeletionSettles(t *testing.T) {
	f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7), presentCalendar(8, calendarItemElsewhere)), maintenanceTime(8, 0),
		[]openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOnBesideRestDays(7, 8))

	f.m.step(context.Background())
	f.advance(calendarDeletionSettle - time.Second)
	f.m.step(context.Background())
	if len(f.writer.batches) != 0 {
		t.Fatalf("closed %v on a deletion read for less than the settle time", f.writer.batches)
	}
	if held := f.m.Stats()[closeOutcomeDeletionUnsettled]; held != 2 {
		t.Fatalf("held closes counted %d, want 2", held)
	}

	f.advance(time.Second)
	f.m.step(context.Background())
	if f.runner.lastErr != nil || len(f.writer.batches) != 1 {
		t.Fatalf("err=%v batches=%v, want the inactive strategy closed once the deletion settled", f.runner.lastErr, f.writer.batches)
	}
}

// A calendar that reads present again, or that no Plan names for a while,
// has its settle time start over when it next reads deleted: a second loss,
// long after the first, does not close on the first one's clock.
func TestACalendarDeletedAgainStartsItsSettleTimeOver(t *testing.T) {
	at := maintenanceTime(8, 0)
	restDays := presentCalendar(8, calendarItemElsewhere)
	for _, tc := range []struct {
		name string
		// between is what the Plans read between the two losses; either way
		// the strategy is active then and nothing closes.
		between func(t *testing.T) []controlplane.MaintenancePlan
	}{
		{"read present again", func(t *testing.T) []controlplane.MaintenancePlan {
			return compiledMaintenancePlans(t, calendarSnapshot(presentCalendar(7, calendarItemCovering(at)), restDays), at, alertDaysOnBesideRestDays(7, 8))
		}},
		{"named by no Plan", func(t *testing.T) []controlplane.MaintenancePlan {
			return compiledMaintenancePlans(t, calendarSnapshot(presentCalendar(8, calendarItemCovering(at))), at, alertDaysOn(8))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7), restDays), at,
				[]openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOnBesideRestDays(7, 8))
			catalog := f.m.catalog.(*maintenanceTestCatalog)
			lost := catalog.plans
			f.m.step(context.Background())

			catalog.plans = tc.between(t)
			f.runner.revision = 2
			f.advance(time.Minute)
			f.m.step(context.Background())

			catalog.plans = lost
			f.runner.revision = 3
			f.advance(calendarDeletionSettle)
			f.m.step(context.Background())
			if len(f.writer.batches) != 0 {
				t.Fatalf("closed %v on the settle time of the first deletion", f.writer.batches)
			}
			f.advance(calendarDeletionSettle)
			f.m.step(context.Background())
			if len(f.writer.batches) != 1 {
				t.Fatalf("batches=%v, want the strategy closed once the second deletion settled", f.writer.batches)
			}
		})
	}
}

// The same calendar read deleted by one Plan and present by another is a
// publication written across a loss or a return, not a deletion. It never
// settles while the two disagree.
func TestACalendarReadDeletedByOnePlanAndPresentByAnotherNeverCloses(t *testing.T) {
	at := maintenanceTime(8, 0)
	restDays := presentCalendar(8, calendarItemElsewhere)
	f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7), restDays), at,
		[]openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOnBesideRestDays(7, 8))
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	present := compiledMaintenancePlans(t, calendarSnapshot(presentCalendar(7, calendarItemCovering(at)), restDays), at, alertDaysOnBesideRestDays(7, 8))
	// The Plan that reads it present is read first, so the later one's
	// deleted read is the one that would stand if the reads were not merged.
	catalog.plans = append(present, catalog.plans...)

	f.m.step(context.Background())
	f.advance(2 * calendarDeletionSettle)
	f.m.step(context.Background())
	if len(f.writer.batches) != 0 {
		t.Fatalf("closed %v on a calendar another Plan still reads present", f.writer.batches)
	}
	if held := f.m.Stats()[closeOutcomeDeletionUnsettled]; held != 2 {
		t.Fatalf("held closes counted %d, want 2", held)
	}
}

// When the calendar source returns, the writer round it returns in reads
// the calendars present on the pages after it and deleted on the ones
// before. A calendar deleted since before the loss has long settled, but no
// deletion closes until the settle time has passed since every calendar
// last read deleted.
func TestTheReturnOfEveryCalendarClosesNothingUntilItSettles(t *testing.T) {
	at := maintenanceTime(8, 0)
	f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7)), at, []openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOn(7))
	lost := f.m.catalog.(*maintenanceTestCatalog).plans
	catalogs, _, second := twoGroups(f, lost, compiledMaintenancePlans(t, calendarSnapshot(deletedCalendar(8)), at, alertDaysOn(8)))
	f.m.step(context.Background())
	f.advance(calendarDeletionSettle + time.Minute)
	f.m.step(context.Background())
	lastLoss := f.now()
	if len(f.writer.batches) != 0 {
		t.Fatalf("setup: closed %v while every calendar reads deleted", f.writer.batches)
	}

	// The return: qg-b's Plans read calendar 8 present and active, qg-a's
	// still read 7 deleted.
	catalogs["qg-b"].plans = compiledMaintenancePlans(t, calendarSnapshot(presentCalendar(8, calendarItemCovering(lastLoss))), lastLoss, alertDaysOn(8))
	second.revision = 2
	f.advance(time.Second)
	f.m.step(context.Background())
	f.advance(calendarDeletionSettle - 2*time.Second)
	f.m.step(context.Background())
	if f.m.groups["qg-b"].timelineRevision != 2 {
		t.Fatal("setup: qg-b was not read again")
	}
	if len(f.writer.batches) != 0 {
		t.Fatalf("closed %v within the settle time of every calendar reading deleted", f.writer.batches)
	}

	f.advance(time.Second)
	f.m.step(context.Background())
	if len(f.writer.batches) != 1 {
		t.Fatalf("batches=%v, want calendar 7's strategy closed once the return settled", f.writer.batches)
	}
}

// A strategy that names no calendar is not held by the others' calendars:
// while every calendar reads deleted, a strategy on time ranges alone that
// is inactive now still closes. The two Plans are the fixture's strategy in
// two Query Groups; only the one that names no calendar may close.
func TestAStrategyNamingNoCalendarClosesWhileEveryCalendarReadsDeleted(t *testing.T) {
	at := maintenanceTime(8, 0)
	f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7)), at, []openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOn(7))
	calendarOnly := f.m.catalog.(*maintenanceTestCatalog).plans
	twoGroups(f, calendarOnly, compiledMaintenancePlans(t, maintenanceReadySnapshot, at))

	f.m.step(context.Background())

	if len(f.writer.batches) != 1 {
		t.Fatalf("batches=%v, want the strategy on time ranges alone closed", f.writer.batches)
	}
	if held := f.m.Stats()[closeOutcomeDeletionUnsettled]; held != 1 {
		t.Fatalf("held closes counted %d, want the calendar strategy's one", held)
	}
}

// maintenanceCatalogByGroup answers each Query Group from its own catalog.
type maintenanceCatalogByGroup map[execution.QueryGroupIdentity]*maintenanceTestCatalog

func (catalogs maintenanceCatalogByGroup) CurrentPlans(ctx context.Context, qg execution.QueryGroupIdentity,
	at execution.EvaluationTime) (controlplane.MaintenancePlans, error) {
	return catalogs[qg].CurrentPlans(ctx, qg, at)
}

// A source-wide loss reaches the owned groups one read at a time, and a
// group that cannot be read again keeps its Plans from before. That old
// read, with its calendar present, does not stand beside the new reads:
// the group read again, now on a deleted calendar, is still not closed
// after the settle time.
func TestTheGroupsReadAfterACalendarLossAreNotClosedOnAnotherGroupsOldRead(t *testing.T) {
	at := maintenanceTime(8, 0)
	f := newMaintenanceTestFixture(t, calendarSnapshot(deletedCalendar(7)), at, []openalerts.Alert{maintenanceAlert("native", "critical")}, alertDaysOn(7))
	afterLoss := f.m.catalog.(*maintenanceTestCatalog).plans
	catalogs, first, second := twoGroups(f,
		compiledMaintenancePlans(t, calendarSnapshot(presentCalendar(7, calendarItemCovering(at))), at, alertDaysOn(7)),
		compiledMaintenancePlans(t, calendarSnapshot(presentCalendar(8, calendarItemCovering(at))), at, alertDaysOn(8)))
	f.m.step(context.Background())
	if len(f.writer.batches) != 0 {
		t.Fatalf("setup: closed %v while the calendars cover the moment", f.writer.batches)
	}

	// The loss: both groups' Plans change; qg-a is read again, qg-b cannot be.
	catalogs["qg-a"].plans = afterLoss
	catalogs["qg-b"].err = errors.New("store unavailable")
	first.revision, second.revision = 2, 2
	f.m.step(context.Background())
	f.advance(calendarDeletionSettle + time.Second)
	f.m.step(context.Background())
	if f.m.groups["qg-a"].timelineRevision != 2 || f.m.groups["qg-b"].timelineRevision != 1 {
		t.Fatal("setup: want qg-a read again and qg-b not")
	}
	if len(f.writer.batches) != 0 {
		t.Fatalf("closed %v on qg-b's read from before the loss", f.writer.batches)
	}
}

// Only groups read under the lease they hold now count. A group whose lease
// has moved still holds its calendars as they were before the change.
func TestAGroupWhoseLeaseMovedDoesNotSpeakForTheCalendars(t *testing.T) {
	read := &maintenanceTestRunner{scope: "obj-a", revision: 2}
	moved := &maintenanceTestRunner{scope: "obj-b", revision: 3}
	groups := map[execution.QueryGroupIdentity]*maintenanceGroup{
		"qg-read":  {contentScope: "obj-a", timelineRevision: 2, calendars: map[int64]bool{7: false}},
		"qg-moved": {contentScope: "obj-b", timelineRevision: 2, calendars: map[int64]bool{7: true}},
	}
	runners := map[execution.QueryGroupIdentity]maintenanceRunner{"qg-read": read, "qg-moved": moved}

	if reads := calendarReads(readUnderCurrentLease(groups, runners)); reads[7] {
		t.Fatal("a group read before its lease moved kept the deleted calendar counted as present")
	}
	moved.revision = 2
	if reads := calendarReads(readUnderCurrentLease(groups, runners)); !reads[7] {
		t.Fatal("with both groups current, a calendar one of them holds present still reads deleted")
	}
}
