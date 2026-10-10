// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisbatch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// hostGroupOf is a host group document with the given host ids as members.
func hostGroupOf(hosts ...int) string {
	ids := make([]string, 0, len(hosts))
	members := make([]string, 0, len(hosts))
	for _, host := range hosts {
		ids = append(ids, fmt.Sprintf(`"%d"`, host))
		members = append(members, fmt.Sprintf(`{"model_id":"cw-Host","model_inst_id":"%d","bk_host_id":%d}`, host, host))
	}
	return `{"model_id":"cw-Host","model_inst_ids":[` + strings.Join(ids, ",") + `],"member_list":[` + strings.Join(members, ",") + `]}`
}

const emptyHostGroup = `{"model_id":"cw-Host","model_inst_ids":[],"member_list":[]}`

// A read over many groups is a window at a time: a window's documents add
// up to at most the bound, or are the one larger document, and each is
// handed over, in order, before the next window is read - the next pipeline
// overwrites a window's replies, so a document handed over late would not
// decode. With documents of one size and a bound of four and a half of
// them, a window is four documents, one pipeline of lengths and one of
// documents, whatever the number of groups: doubling the groups doubles the
// round trips, not what is held at once. A transport failure part way
// returns the error after the windows before it were handed over.
func TestAGroupReadIsAWindowAtATimeAndAFailurePartWayReturnsTheError(t *testing.T) {
	for _, count := range []int{40, 80} {
		client := &groupClient{values: map[string]string{}}
		ids := make([]string, 0, count)
		for index := 0; index < count; index++ {
			id := fmt.Sprint(1000 + index)
			ids = append(ids, id)
			client.values["p:dynamic_group:"+id] = hostGroupOf(100 + index)
		}
		size := len(client.values["p:dynamic_group:1000"])
		reader, err := NewGroupReader(client, "p:")
		if err != nil {
			t.Fatal(err)
		}
		visited, ahead := []string{}, 0
		visit := func(id string, read GroupRead) {
			if snapshot := snapshotOf(id, read, time.Time{}); len(snapshot.Members) != 1 {
				t.Fatalf("%d groups: %s handed over as %+v", count, id, snapshot)
			}
			visited = append(visited, id)
			ahead = max(ahead, client.gets()-len(visited))
		}
		if err := reader.Read(context.Background(), ids, size*9/2, visit); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(visited) != fmt.Sprint(ids) || ahead != 3 {
			t.Fatalf("%d groups: handed over %d, at most %d read ahead; want all in order, 3 ahead", count, len(visited), ahead)
		}
		if len(client.calls) != count/4 || client.strlens != count {
			t.Fatalf("%d groups: %d pipelines of documents, %d lengths; want %d and %d", count, len(client.calls), client.strlens, count/4, count)
		}
		for _, call := range client.calls {
			if len(call) != 4 {
				t.Fatalf("%d groups: a window of %d documents, want 4", count, len(call))
			}
		}

		client.calls, client.failAt, visited = nil, 2, nil
		if err := reader.Read(context.Background(), ids, size*9/2, visit); err == nil || len(visited) != 4 {
			t.Fatalf("%d groups: a read whose second window failed handed over %d and returned %v; want 4 and the error", count, len(visited), err)
		}
	}
}

// A document larger than the bound is read alone, and read: every read
// makes progress. A key Redis answers with an error says nothing about its
// group: it is not handed over, the groups beside it are, and the read
// returns an error counting it.
func TestALargeDocumentIsReadAloneAndAnAnsweredKeyIsNotHandedOver(t *testing.T) {
	client := &groupClient{values: map[string]string{
		"p:dynamic_group:1": hostGroupOf(101), "p:dynamic_group:2": hostGroupOf(102), "p:dynamic_group:3": hostGroupOf(103)},
		answered: map[string]error{"p:dynamic_group:2": answeredError("WRONGTYPE Operation against a key holding the wrong kind of value")}}
	reader, err := NewGroupReader(client, "p:")
	if err != nil {
		t.Fatal(err)
	}
	handed := []string{}
	err = reader.Read(context.Background(), []string{"1", "2", "3"}, 1, func(id string, read GroupRead) {
		handed = append(handed, fmt.Sprintf("%s=%d", id, len(snapshotOf(id, read, time.Time{}).Members)))
	})
	var unanswered *redisbatch.UnansweredError
	if !errors.As(err, &unanswered) || unanswered.Keys != 1 || fmt.Sprint(handed) != "[1=1 3=1]" || len(client.calls) != 3 {
		t.Fatalf("handed %v in %d pipelines of documents and returned %v; want 1 and 3, one a pipeline, and group 2 unanswered",
			handed, len(client.calls), err)
	}
}

// answerEvery has Redis answer every group's key with err, or, nil, answer
// them again.
func (fixture *groupFixture) answerEvery(err error) {
	fixture.client.answered = map[string]error{}
	if err == nil {
		return
	}
	for _, id := range fixture.ids {
		fixture.client.answered["p:dynamic_group:"+id] = err
	}
}

// Redis answering every key with an error - LOADING while it restarts -
// says nothing about the groups: the refresh keeps every snapshot, serves
// each as past a failed refresh, fails, and counts the groups it could not
// read. Answered again, the groups are read as before.
func TestARefreshRedisAnswersEveryKeyWithAnErrorKeepsEverySnapshot(t *testing.T) {
	fixture := newGroupFixture(t, 3)
	fixture.answerEvery(answeredError("LOADING Redis is loading the dataset in memory"))
	fixture.now = fixture.now.Add(time.Minute)
	err := fixture.store.Refresh(context.Background())
	var unanswered *redisbatch.UnansweredError
	if !errors.As(err, &unanswered) || unanswered.Keys != 3 {
		t.Fatalf("the refresh returned %v, want every group unanswered", err)
	}
	for _, id := range fixture.ids {
		lookup := fixture.store.Group(context.Background(), id, time.Minute)
		if lookup.Snapshot == nil || lookup.Snapshot.Unavailable != "" || len(lookup.Snapshot.Members) != 1 || !lookup.RefreshFailed {
			t.Fatalf("group %s after an unanswered refresh = %+v, %+v; want its members, past a failed refresh", id, lookup, lookup.Snapshot)
		}
	}
	if health := fixture.store.Health(); !health.RefreshFailed || health.Unanswered != 3 || health.UnansweredReads != 3 ||
		health.ConsecutiveErrors != 1 || health.Loaded != 3 {
		t.Fatalf("health after an unanswered refresh = %+v", health)
	}

	fixture.answerEvery(nil)
	fixture.now = fixture.now.Add(time.Minute)
	if err := fixture.store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lookup := fixture.store.Group(context.Background(), fixture.ids[0], time.Minute); lookup.RefreshFailed || lookup.Age != 0 {
		t.Fatalf("group after an answered refresh = %+v", lookup)
	}
	if health := fixture.store.Health(); health.RefreshFailed || health.Unanswered != 0 || health.UnansweredReads != 3 {
		t.Fatalf("health after an answered refresh = %+v", health)
	}
}

// The health names each group served past refreshes that could not read
// it: since the first of them after its last read, which a second does not
// move, and why the latest could not, in closed words - the code Redis
// answered its key with, or transport. A group never read is not served past anything and is not
// named; a read of the group, or its reference ageing out, ends its run.
func TestTheHealthNamesEachGroupServedPastAFailedRefresh(t *testing.T) {
	fixture := newGroupFixture(t, 2)
	loading := answeredError("LOADING Redis is loading the dataset in memory")
	// A group whose key Redis answers with an error throughout is never read.
	fixture.client.values["p:dynamic_group:never"] = hostGroupOf(9)
	answer := func(answered map[string]error) {
		answered["p:dynamic_group:never"] = loading
		fixture.client.answered = answered
	}
	answer(map[string]error{})
	if lookup := fixture.store.Group(context.Background(), "never", time.Minute); lookup.ReadErr == nil {
		t.Fatal("setup: a group never read was read")
	}
	answer(map[string]error{"p:dynamic_group:" + fixture.ids[0]: loading, "p:dynamic_group:" + fixture.ids[1]: loading})
	fixture.now = fixture.now.Add(time.Minute)
	started := fixture.now
	_ = fixture.store.Refresh(context.Background())
	failing := fixture.store.Health().Failing
	if len(failing) != 2 || failing[0].ID != fixture.ids[0] || failing[1].ID != fixture.ids[1] || !failing[0].Since.Equal(started) ||
		failing[0].Reason != "LOADING" {
		t.Fatalf("failing after an unanswered refresh = %+v", failing)
	}

	answer(map[string]error{})
	fixture.client.err = errors.New("connection refused")
	fixture.now = fixture.now.Add(time.Minute)
	_ = fixture.store.Refresh(context.Background())
	failing = fixture.store.Health().Failing
	if len(failing) != 2 || !failing[1].Since.Equal(started) || failing[1].Reason != "transport" {
		t.Fatalf("failing after a refresh that failed at the transport = %+v, want the run's start kept and the latest reason", failing)
	}

	fixture.client.err = nil
	answer(map[string]error{"p:dynamic_group:" + fixture.ids[1]: answeredError("WRONGTYPE Operation against a key holding the wrong kind of value")})
	fixture.now = fixture.now.Add(time.Minute)
	_ = fixture.store.Refresh(context.Background())
	if failing = fixture.store.Health().Failing; len(failing) != 1 || failing[0].ID != fixture.ids[1] || failing[0].Reason != "WRONGTYPE" {
		t.Fatalf("failing after the first group read again = %+v, want only the second", failing)
	}

	// Nobody asks for the second group any more: past its horizon it leaves
	// the refresh and the health, run and all.
	for step := 0; step < 12; step++ {
		fixture.now = fixture.now.Add(time.Minute)
		_ = fixture.store.Refresh(context.Background())
		fixture.store.Group(context.Background(), fixture.ids[0], time.Minute)
	}
	if failing = fixture.store.Health().Failing; len(failing) != 0 {
		t.Fatalf("failing after the group aged out = %+v, want none", failing)
	}
}

// One group Redis answers with an error keeps its snapshot and is served as
// past a failed refresh; the groups beside it are refreshed as usual.
func TestAGroupRedisAnswersWithAnErrorKeepsOnlyItsOwnSnapshot(t *testing.T) {
	fixture := newGroupFixture(t, 3)
	for _, id := range fixture.ids {
		fixture.client.values["p:dynamic_group:"+id] = hostGroupOf(500, 501)
	}
	fixture.client.answered = map[string]error{
		"p:dynamic_group:" + fixture.ids[1]: answeredError("WRONGTYPE Operation against a key holding the wrong kind of value")}
	fixture.now = fixture.now.Add(time.Minute)
	if err := fixture.store.Refresh(context.Background()); err == nil {
		t.Fatal("a refresh with a group unanswered did not fail")
	}
	for index, id := range fixture.ids {
		lookup := fixture.store.Group(context.Background(), id, time.Minute)
		want, failed := 2, false
		if index == 1 {
			want, failed = 1, true
		}
		if len(lookup.Snapshot.Members) != want || lookup.RefreshFailed != failed {
			t.Fatalf("group %s = %d members, refresh failed %v; want %d, %v", id, len(lookup.Snapshot.Members), lookup.RefreshFailed, want, failed)
		}
	}
}

// A group's first read that Redis answers with an error publishes nothing:
// the lookup says the read failed, and the next ask reads it again.
func TestAFirstReadRedisAnswersWithAnErrorPublishesNothing(t *testing.T) {
	client := &groupClient{values: map[string]string{"p:dynamic_group:7": hostGroupOf(7)},
		answered: map[string]error{"p:dynamic_group:7": answeredError("LOADING Redis is loading the dataset in memory")}}
	reader, err := NewGroupReader(client, "p:")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound})
	if err != nil {
		t.Fatal(err)
	}
	if lookup := store.Group(context.Background(), "7", time.Minute); lookup.ReadErr == nil || lookup.Snapshot != nil {
		t.Fatalf("a first read Redis answered with an error = %+v, want the error and no snapshot", lookup)
	}
	if health := store.Health(); health.Loaded != 0 || health.Unavailable != 0 {
		t.Fatalf("health after it = %+v, want nothing held", health)
	}
	client.answered = nil
	if lookup := store.Group(context.Background(), "7", time.Minute); lookup.ReadErr != nil || lookup.Snapshot == nil ||
		len(lookup.Snapshot.Members) != 1 || store.Health().SyncReads != 2 {
		t.Fatalf("the next ask = %+v after %d reads, want the group read again", lookup, store.Health().SyncReads)
	}
}

// An emptying pending past its settle time is not believed from a refresh
// Redis answered the group with an error: the group keeps the snapshot it
// had and stays pending, and is believed once it is read empty again.
func TestAPendingEmptyingIsNotBelievedFromAnUnansweredRead(t *testing.T) {
	fixture := newGroupFixture(t, 1)
	fixture.write(fixture.ids, true)
	fixture.refresh(t, 10)
	if !fixture.servesMembers(t, fixture.ids[0]) {
		t.Fatal("setup: the emptying was believed before its settle time")
	}
	fixture.answerEvery(answeredError("LOADING Redis is loading the dataset in memory"))
	fixture.now = fixture.now.Add(time.Minute)
	if err := fixture.store.Refresh(context.Background()); err == nil {
		t.Fatal("an unanswered refresh did not fail")
	}
	if !fixture.servesMembers(t, fixture.ids[0]) {
		t.Fatal("an emptying was believed from a refresh that could not read the group")
	}
	fixture.answerEvery(nil)
	fixture.refresh(t, 1)
	if lookup := fixture.store.Group(context.Background(), fixture.ids[0], time.Minute); len(lookup.Snapshot.Members) != 0 || lookup.EmptiedHeld {
		t.Fatalf("the emptying read again after its settle time = %+v, want it believed", lookup)
	}
}

// A group first read empty while another's emptying was pending waits
// with it, and past its settle time is not believed from a refresh Redis
// answered it with an error either: it stays held back.
func TestAnUnconfirmedGroupIsNotBelievedFromAnUnansweredRead(t *testing.T) {
	fixture := newGroupFixture(t, 2)
	fixture.write(fixture.ids[:1], true)
	fixture.refresh(t, 1)
	fixture.client.values["p:dynamic_group:9999"] = emptyHostGroup
	fixture.ids = append(fixture.ids, "9999")
	if lookup := fixture.store.Group(context.Background(), "9999", time.Minute); lookup.Snapshot.Unavailable != targetplan.ReasonEmptiedHeld {
		t.Fatalf("setup: a group first read empty during a pending emptying = %+v", lookup.Snapshot)
	}
	fixture.write(fixture.ids[:1], false)
	fixture.refresh(t, 10)
	fixture.client.answered = map[string]error{"p:dynamic_group:9999": answeredError("LOADING Redis is loading the dataset in memory")}
	fixture.now = fixture.now.Add(time.Minute)
	if err := fixture.store.Refresh(context.Background()); err == nil {
		t.Fatal("an unanswered refresh did not fail")
	}
	if lookup := fixture.store.Group(context.Background(), "9999", time.Minute); lookup.Snapshot == nil ||
		lookup.Snapshot.Unavailable != targetplan.ReasonEmptiedHeld {
		t.Fatalf("an unconfirmed group read unanswered past its settle time = %+v, want it still held", lookup)
	}
}

// A refresh decodes each window as it is read: with a bound of four and a
// half documents over forty groups, every group comes out with its member,
// none decoded from a window the next one overwrote.
func TestARefreshDecodesEachWindowBeforeReadingTheNext(t *testing.T) {
	fixture := newGroupFixture(t, 40)
	size := len(fixture.client.values["p:dynamic_group:"+fixture.ids[0]])
	fixture.store.readBound = size * 9 / 2
	fixture.client.calls = nil
	fixture.refresh(t, 1)
	if len(fixture.client.calls) != 10 {
		t.Fatalf("the refresh read %d windows, want 10 of 4", len(fixture.client.calls))
	}
	for _, id := range fixture.ids {
		if lookup := fixture.store.Group(context.Background(), id, time.Minute); lookup.Snapshot == nil ||
			lookup.Snapshot.Unavailable != "" || len(lookup.Snapshot.Members) != 1 {
			t.Fatalf("group %s after the refresh = %+v", id, lookup.Snapshot)
		}
	}
}

// A store without a read bound is refused: a bound of zero would read one
// document a round trip, and no bound at all every document at once.
func TestAGroupStoreNeedsAReadBound(t *testing.T) {
	reader, err := NewGroupReader(&groupClient{}, "p:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute}); err == nil {
		t.Fatal("a store without a read bound was built")
	}
}

// groupFixture is a store over count host groups, each referenced and read
// with a member, and the client behind it. Its refreshes are a minute apart
// and every group is asked for after each, as its Plans ask every Slot.
type groupFixture struct {
	client *groupClient
	store  *GroupStore
	now    time.Time
	ids    []string
	said   [][2]int
}

func newGroupFixture(t *testing.T, count int) *groupFixture {
	t.Helper()
	fixture := &groupFixture{client: &groupClient{values: map[string]string{}}, now: time.Unix(1000, 0)}
	reader, err := NewGroupReader(fixture.client, "p:")
	if err != nil {
		t.Fatal(err)
	}
	fixture.store, err = NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound,
		Now:            func() time.Time { return fixture.now },
		EmptiedChanged: func(held, candidates int) { fixture.said = append(fixture.said, [2]int{held, candidates}) }})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < count; index++ {
		id := fmt.Sprint(2000 + index)
		fixture.ids = append(fixture.ids, id)
		fixture.client.values["p:dynamic_group:"+id] = hostGroupOf(index + 1)
		if lookup := fixture.store.Group(context.Background(), id, time.Minute); lookup.Snapshot == nil || len(lookup.Snapshot.Members) != 1 {
			t.Fatalf("setup: group %s = %+v", id, lookup)
		}
	}
	return fixture
}

// write writes each of the ids' documents: empty, or its members back.
func (fixture *groupFixture) write(ids []string, empty bool) {
	for _, id := range ids {
		if empty {
			fixture.client.values["p:dynamic_group:"+id] = emptyHostGroup
			continue
		}
		var host int
		_, _ = fmt.Sscan(id, &host)
		fixture.client.values["p:dynamic_group:"+id] = hostGroupOf(host - 1999)
	}
}

// refresh refreshes times times, a minute apart, asking for every group
// after each.
func (fixture *groupFixture) refresh(t *testing.T, times int) {
	t.Helper()
	for step := 0; step < times; step++ {
		fixture.now = fixture.now.Add(time.Minute)
		if err := fixture.store.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, id := range fixture.ids {
			fixture.store.Group(context.Background(), id, time.Minute)
		}
	}
}

// selector is how a Plan of the one group resolves it now.
func (fixture *groupFixture) selector(id string) targetplan.SelectorResult {
	resolver := NewTargetResolver(fixture.store, nil, func() time.Time { return fixture.now })
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
		DynamicGroups: []string{id}}
	return resolver.Resolve(context.Background(), plan, time.Minute).Selectors[0]
}

// servesMembers is whether the store still serves the group's members, held
// back, rather than its empty read.
func (fixture *groupFixture) servesMembers(t *testing.T, id string) bool {
	t.Helper()
	lookup := fixture.store.Group(context.Background(), id, time.Minute)
	if len(lookup.Snapshot.Members) == 1 && !lookup.EmptiedHeld {
		t.Fatalf("group %s serves members without being held: %+v", id, lookup)
	}
	return len(lookup.Snapshot.Members) == 1
}

// Every group that had members read empty at once is the writer answering
// nothing: the store holds back the reads, serves the snapshots before as
// past a failed refresh, says so once, and past the staleness bound names
// the groups emptied_held. It holds past a writer cycle. When the writer
// writes the members back, they are published and the end is said once.
func TestEveryGroupEmptyingAtOnceIsHeldAsTheWritersAndReleasedWhenItComesBack(t *testing.T) {
	fixture := newGroupFixture(t, 3)
	fixture.write(fixture.ids, true)
	fixture.refresh(t, 1)
	for _, id := range fixture.ids {
		lookup := fixture.store.Group(context.Background(), id, time.Minute)
		if len(lookup.Snapshot.Members) != 1 || !lookup.EmptiedHeld || !lookup.RefreshFailed {
			t.Fatalf("group %s after every group emptied = %+v, want the snapshot before, held and served past the refresh", id, lookup)
		}
	}
	if health := fixture.store.Health(); health.EmptiedHeld != 3 || health.EmptiedPending != 3 || health.EmptiedHolds != 1 {
		t.Fatalf("health = %+v, want 3 held by 1 refresh", health)
	}
	if len(fixture.said) != 1 || fixture.said[0] != [2]int{3, 3} {
		t.Fatalf("said %v, want once: 3 held of 3 that had members", fixture.said)
	}

	fixture.refresh(t, 10)
	if selector := fixture.selector(fixture.ids[0]); selector.State != targetplan.SelectorUnavailable || selector.Reason != targetplan.ReasonEmptiedHeld {
		t.Fatalf("selector past the staleness bound = %+v, want unavailable as emptied_held", selector)
	}
	if fixture.store.Health().EmptiedHeld != 3 || len(fixture.said) != 1 {
		t.Fatalf("a hold past a writer cycle let go or was said again: %+v, %v", fixture.store.Health(), fixture.said)
	}

	fixture.write(fixture.ids, false)
	fixture.refresh(t, 1)
	lookup := fixture.store.Group(context.Background(), fixture.ids[0], time.Minute)
	if lookup.EmptiedHeld || lookup.RefreshFailed || len(lookup.Snapshot.Members) != 1 {
		t.Fatalf("group after the writer came back = %+v, want a fresh read of its members", lookup)
	}
	if health := fixture.store.Health(); health.EmptiedHeld != 0 || health.EmptiedPending != 0 {
		t.Fatalf("health after the writer came back = %+v", health)
	}
	if len(fixture.said) != 2 || fixture.said[1] != [2]int{0, 3} {
		t.Fatalf("said %v, want the hold's end once", fixture.said)
	}
}

// One group emptying among several is that group emptying - a service
// retired, a condition that matches nothing any more. It waits one writer
// cycle with the snapshot before served, never resolving OKEmpty in that
// time, then is published empty so its former members' alerts can close. The
// groups beside it are untouched and nothing is said.
func TestOneGroupEmptyingIsBelievedAfterAWriterCycle(t *testing.T) {
	fixture := newGroupFixture(t, 3)
	fixture.write(fixture.ids[:1], true)
	// First read empty at minute 1; the cycle is out at minute 11.
	for minute := 1; minute <= 10; minute++ {
		fixture.refresh(t, 1)
		if !fixture.servesMembers(t, fixture.ids[0]) {
			t.Fatalf("minute %d: the group that emptied was believed before a writer cycle", minute)
		}
	}
	if health := fixture.store.Health(); health.EmptiedPending != 1 || health.EmptiedHeld != 0 {
		t.Fatalf("health while it waits = %+v, want 1 pending and none held", health)
	}
	fixture.refresh(t, 1)
	if fixture.servesMembers(t, fixture.ids[0]) {
		t.Fatal("the group that emptied was not believed after a writer cycle")
	}
	if selector := fixture.selector(fixture.ids[0]); selector.State != targetplan.SelectorOKEmpty {
		t.Fatalf("selector = %+v, want OKEmpty", selector)
	}
	if beside := fixture.store.Group(context.Background(), fixture.ids[1], time.Minute); beside.EmptiedHeld || len(beside.Snapshot.Members) != 1 {
		t.Fatalf("the group beside it moved: %+v", beside)
	}
	if len(fixture.said) != 0 || fixture.store.Health().EmptiedHolds != 0 {
		t.Fatalf("a group emptying alone was said or held: %v, %+v", fixture.said, fixture.store.Health())
	}
}

// An emptying whose writes land over two refreshes is judged as one: of five
// groups, three empty and then two, and all five are held, none read empty
// first; of fifty, ten a refresh, every one is held from the second refresh
// on and none is believed.
func TestAnEmptyingLandingOverSeveralRefreshesIsJudgedAsOne(t *testing.T) {
	fixture := newGroupFixture(t, 5)
	fixture.write(fixture.ids[:3], true)
	fixture.refresh(t, 1)
	fixture.write(fixture.ids[3:], true)
	fixture.refresh(t, 1)
	if held := fixture.store.Health().EmptiedHeld; held != 5 {
		t.Fatalf("held %d of 5 emptied over two refreshes, want all", held)
	}
	fixture.refresh(t, 10)
	for _, id := range fixture.ids {
		if !fixture.servesMembers(t, id) {
			t.Fatalf("group %s was read empty", id)
		}
	}

	fixture = newGroupFixture(t, 50)
	for step := 0; step < 5; step++ {
		fixture.write(fixture.ids[10*step:10*step+10], true)
		fixture.refresh(t, 1)
		if held := fixture.store.Health().EmptiedHeld; step >= 1 && held != 10*(step+1) {
			t.Fatalf("after %d refreshes held %d, want %d", step+1, held, 10*(step+1))
		}
	}
	fixture.refresh(t, 10)
	for _, id := range fixture.ids {
		if !fixture.servesMembers(t, id) {
			t.Fatalf("group %s of fifty was read empty", id)
		}
	}
}

// Some held groups coming back lets only those go: the rest are still held,
// served with their members and never read empty, and their wait starts
// over, since the writer writing members back is partway through a cycle.
// If they come back too, they are published; if they really are empty, they
// are believed a writer cycle after the last group came back.
func TestHeldGroupsComingBackOneByOneLetOnlyThoseGo(t *testing.T) {
	for _, test := range []struct {
		groups, back int
	}{{groups: 3, back: 1}, {groups: 50, back: 35}} {
		t.Run(fmt.Sprintf("%d of %d", test.back, test.groups), func(t *testing.T) {
			fixture := newGroupFixture(t, test.groups)
			fixture.write(fixture.ids, true)
			fixture.refresh(t, 5)
			fixture.write(fixture.ids[:test.back], false)
			fixture.refresh(t, 1)
			rest := fixture.ids[test.back:]
			for _, id := range rest {
				if !fixture.servesMembers(t, id) {
					t.Fatalf("group %s still out was read empty when others came back", id)
				}
			}
			fixture.refresh(t, 9)
			if !fixture.servesMembers(t, rest[0]) {
				t.Fatal("the wait did not start over when others came back")
			}

			// They really are empty: believed a cycle after the others came back.
			fixture.refresh(t, 1)
			if fixture.servesMembers(t, rest[0]) {
				t.Fatal("a group still empty a writer cycle after the rest came back was never believed")
			}
		})
	}

	fixture := newGroupFixture(t, 3)
	fixture.write(fixture.ids, true)
	fixture.refresh(t, 2)
	fixture.write(fixture.ids[:1], false)
	fixture.refresh(t, 1)
	fixture.write(fixture.ids[1:], false)
	fixture.refresh(t, 1)
	if health := fixture.store.Health(); health.EmptiedPending != 0 || health.EmptiedHeld != 0 {
		t.Fatalf("after every group came back = %+v, want nothing held", health)
	}
}

// A group first read empty while others are held has no snapshot before it
// and may be one of them: it is emptied_held, not empty. Once the hold ends
// and it has read empty a writer cycle, it is believed.
func TestAGroupFirstReadDuringAHoldIsNotReadEmpty(t *testing.T) {
	fixture := newGroupFixture(t, 3)
	fixture.write(fixture.ids, true)
	fixture.refresh(t, 1)
	fixture.client.values["p:dynamic_group:9999"] = emptyHostGroup
	fixture.ids = append(fixture.ids, "9999")
	if selector := fixture.selector("9999"); selector.State != targetplan.SelectorUnavailable || selector.Reason != targetplan.ReasonEmptiedHeld {
		t.Fatalf("a group first read during a hold = %+v, want unavailable as emptied_held", selector)
	}
	// The hold outlasts a writer cycle: the group waits with it.
	fixture.refresh(t, 11)
	if selector := fixture.selector("9999"); selector.Reason != targetplan.ReasonEmptiedHeld {
		t.Fatalf("while the hold outlasts a cycle = %+v, want still emptied_held", selector)
	}
	// The others come back, and its wait starts over from that refresh: it
	// is still waiting a cycle later less a minute, and read empty after.
	fixture.write(fixture.ids[:3], false)
	fixture.refresh(t, 10)
	if selector := fixture.selector("9999"); selector.Reason != targetplan.ReasonEmptiedHeld {
		t.Fatalf("before a writer cycle from the return = %+v, want still emptied_held", selector)
	}
	fixture.refresh(t, 1)
	if selector := fixture.selector("9999"); selector.State != targetplan.SelectorOKEmpty {
		t.Fatalf("a writer cycle after the return = %+v, want OKEmpty", selector)
	}
}

// A group first read empty while another group's emptying is pending -
// under the line, not held - waits with it: the one pending may be the start
// of an emptying whose rest has not landed yet.
func TestAGroupFirstReadWhileAnEmptyingIsPendingIsNotReadEmpty(t *testing.T) {
	fixture := newGroupFixture(t, 3)
	fixture.write(fixture.ids[:1], true)
	fixture.refresh(t, 1)
	if health := fixture.store.Health(); health.EmptiedPending != 1 || health.EmptiedHeld != 0 {
		t.Fatalf("setup: health = %+v, want one pending and none held", health)
	}
	fixture.client.values["p:dynamic_group:9999"] = emptyHostGroup
	fixture.ids = append(fixture.ids, "9999")
	if selector := fixture.selector("9999"); selector.State != targetplan.SelectorUnavailable || selector.Reason != targetplan.ReasonEmptiedHeld {
		t.Fatalf("a group first read while an emptying is pending = %+v, want unavailable as emptied_held", selector)
	}
}

// The line, on both sides of each of its numbers: every group that had
// members (at least two), or at least 20 and more than a fifth of them. Past
// a writer cycle, a held emptying still serves its members and one below the
// line is read empty.
func TestAnEmptyingIsHeldOnlyPastTheLine(t *testing.T) {
	for _, test := range []struct {
		groups, emptied int
		held            bool
	}{
		{groups: 100, emptied: 20, held: false}, // a fifth exactly is not more than a fifth
		{groups: 100, emptied: 21, held: true},
		{groups: 90, emptied: 19, held: false}, // more than a fifth, under the floor
		{groups: 90, emptied: 20, held: true},
		{groups: 2, emptied: 2, held: true}, // every one, and at least two
		{groups: 3, emptied: 2, held: false},
		{groups: 1, emptied: 1, held: false}, // a store of one group cannot tell
	} {
		t.Run(fmt.Sprintf("%d of %d", test.emptied, test.groups), func(t *testing.T) {
			fixture := newGroupFixture(t, test.groups)
			fixture.write(fixture.ids[:test.emptied], true)
			fixture.refresh(t, 1)
			held := fixture.store.Health().EmptiedHeld
			if (held == test.emptied) != test.held || (held != 0 && held != test.emptied) {
				t.Fatalf("held %d, want held %v", held, test.held)
			}
			fixture.refresh(t, 10)
			if serves := fixture.servesMembers(t, fixture.ids[0]); serves != test.held {
				t.Fatalf("past a writer cycle serves members %v, want %v", serves, test.held)
			}
		})
	}
}

// Known boundary, pinned so that changing it is a decision: when every group
// a store references really empties, the hold does not end on its own. It
// ends when members come back (above), when nothing references the groups
// any more, or when the process restarts and reads what the writer has.
func TestEveryGroupReallyEmptyingIsHeldUntilItsReferencesOrTheProcessGo(t *testing.T) {
	fixture := newGroupFixture(t, 2)
	fixture.write(fixture.ids, true)
	fixture.refresh(t, 180)
	if health := fixture.store.Health(); health.EmptiedHeld != 2 || health.EmptiedHolds != 180 {
		t.Fatalf("after 180 refreshes = %+v, want still held, counted every refresh", health)
	}

	// Nothing references them any more.
	for step := 0; step < 11; step++ {
		fixture.now = fixture.now.Add(time.Minute)
		if err := fixture.store.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if health := fixture.store.Health(); health.EmptiedPending != 0 || health.EmptiedHeld != 0 || health.Referenced != 0 {
		t.Fatalf("after the references went = %+v, want nothing held", health)
	}

	// A new process reads what the writer has.
	restarted := newGroupFixture(t, 0)
	restarted.client.values["p:dynamic_group:1"] = emptyHostGroup
	if selector := restarted.selector("1"); selector.State != targetplan.SelectorOKEmpty {
		t.Fatalf("after a restart = %+v, want OKEmpty", selector)
	}
}

// A group whose members were all refused is not empty - it resolves
// incomplete - so it is not counted towards an emptying and not held back.
func TestAGroupWhoseMembersWereAllRefusedIsNotAnEmptying(t *testing.T) {
	fixture := newGroupFixture(t, 2)
	fixture.client.values["p:dynamic_group:"+fixture.ids[0]] = emptyHostGroup
	fixture.client.values["p:dynamic_group:"+fixture.ids[1]] = `{"model_id":"cw-Host","member_list":[{"model_id":"cw-MySQL","model_inst_id":"db-1"}]}`
	fixture.refresh(t, 1)
	if health := fixture.store.Health(); health.EmptiedHeld != 0 || health.EmptiedPending != 1 {
		t.Fatalf("health = %+v: one emptied and one refused is one pending and nothing held", health)
	}
	if lookup := fixture.store.Group(context.Background(), fixture.ids[1], time.Minute); lookup.EmptiedHeld || lookup.Snapshot.Dropped != 1 || len(lookup.Snapshot.Members) != 0 {
		t.Fatalf("the refused group = %+v, want its read published with the member dropped", lookup)
	}
}

// Why a refresh could not read a group is said in closed words, never the
// error's text: the code Redis answered with, redis_error for a reply that
// leads with none, timed_out past a deadline or on a cancel, and transport
// for the rest - a dial failure among them, whose text names the endpoint.
func TestAGroupFailureIsNamedInClosedWords(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
		Err: errors.New("connect: connection refused")}
	for _, test := range []struct {
		err  error
		want string
	}{
		{err: answeredError("LOADING Redis is loading the dataset in memory"), want: "LOADING"},
		{err: fmt.Errorf("p:dynamic_group:7: %w", answeredError("WRONGTYPE Operation against a key holding the wrong kind of value")), want: "WRONGTYPE"},
		{err: answeredError("something went wrong at 127.0.0.1:1"), want: "redis_error"},
		{err: fmt.Errorf("alarmd cmdbcache: read dynamic groups: %w", context.DeadlineExceeded), want: "timed_out"},
		{err: context.Canceled, want: "timed_out"},
		{err: &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, want: "timed_out"},
		{err: fmt.Errorf("alarmd cmdbcache: read dynamic groups: %w", dial), want: "transport"},
	} {
		if got := groupFailureReason(test.err); got != test.want {
			t.Fatalf("%v = %q, want %q", test.err, got, test.want)
		}
	}
}
