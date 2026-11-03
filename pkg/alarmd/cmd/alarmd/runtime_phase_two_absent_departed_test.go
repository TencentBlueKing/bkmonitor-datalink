package main

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

const departedKey = "bkmonitor.cache.departed_strategies"

// markStrategy writes a mark as the strategy list's writer does
// (runtime_cache_contract.encode_departed_strategy: compact JSON, keys
// sorted).
func markStrategy(t *testing.T, client *redis.Client, id, state string, revision int) {
	t.Helper()
	value := `{"bk_tenant_id":"system","departed_at":1793261928,"record_revision":` + strconv.Itoa(revision) + `,"state":"` + state + `","version":1}`
	if err := client.HSet(context.Background(), departedKey, id, value).Err(); err != nil {
		t.Fatal(err)
	}
}

// withMarks gives the fixture's loop the source's own reader of the marks,
// on a Redis the test writes the marks to.
func withMarks(t *testing.T, fixture *absentTestFixture) *redis.Client {
	t.Helper()
	_, client := startPhaseTwoRedis(t)
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	fixture.loop.marks = source
	return client
}

func (fixture *absentTestFixture) reason(id string) string {
	fixture.link.mu.Lock()
	defer fixture.link.mu.Unlock()
	return fixture.link.alerts[id].reason
}

// A strategy disabled or deleted under the writer's mark is closed on the
// round after the first one that read it so - one round, not the implicit
// path's three, and with no second observation - with the platform's own
// sentence for a strategy disabled or deleted. The two paths are counted
// apart.
func TestAMarkedStrategyClosesOnTheNextRound(t *testing.T) {
	for _, state := range []string{absentalerts.MarkDisabled, absentalerts.MarkDeleted} {
		want := "策略已被停用或删除，告警关闭"
		fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
		client := withMarks(t, fixture)
		markStrategy(t, client, "10", state, 3)
		ctx := context.Background()
		fixture.loop.step(ctx)
		if len(fixture.link.closed()) != 0 || fixture.loop.Stats()[absentalerts.OutcomeDepartedWaiting] != 1 {
			t.Fatalf("%s: the first marked round closed, or did not wait: %+v", state, fixture.loop.Stats())
		}
		fixture.now = fixture.now.Add(absentCloseInterval)
		fixture.loop.step(ctx)
		stats := fixture.loop.Stats()
		if fixture.link.closed()["alert-1"] != 1 || stats[absentalerts.OutcomeDepartedCloseDecided] != 1 || stats[absentalerts.OutcomeCloseDecided] != 0 ||
			stats[absentalerts.OutcomeWithinGrace] != 0 {
			t.Fatalf("%s: the second marked round did not close on the fast path: %+v closed %+v", state, stats, fixture.link.closed())
		}
		if got := fixture.reason("alert-1"); got != want {
			t.Fatalf("%s: reason %q, want %q", state, got, want)
		}
	}
}

// Disabled and enabled again before the close: enabling removes the mark,
// raises the record's revision and lists the strategy again. Whether that
// lands between the two marked rounds, or between the round's decision and
// its close (the re-check reads the snapshot and the marks again), nothing
// is closed, and each is counted by what the re-check found.
func TestAStrategyEnabledAgainBeforeItsCloseIsNotClosed(t *testing.T) {
	listing10 := func(fixture *absentTestFixture) {
		fixture.control.snapshot = liveSnapshot("observation-one", fixture.now, 100)
		fixture.control.snapshot.Strategies = append(fixture.control.snapshot.Strategies,
			controlplane.DepartedStrategy{TenantID: "system", StrategyID: "10", BusinessID: 2})
	}
	t.Run("between the rounds", func(t *testing.T) {
		fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
		client := withMarks(t, fixture)
		markStrategy(t, client, "10", absentalerts.MarkDisabled, 3)
		ctx := context.Background()
		fixture.loop.step(ctx)
		client.HDel(ctx, departedKey, "10")
		listing10(fixture)
		for i := 0; i < 4; i++ {
			fixture.now = fixture.now.Add(absentCloseInterval)
			fixture.loop.step(ctx)
		}
		if len(fixture.link.closed()) != 0 || fixture.loop.Difference()["candidates"] != 0 {
			t.Fatalf("a strategy enabled again was closed or is still a candidate: %+v", fixture.link.closed())
		}
	})
	for name, enable := range map[string]func(*absentTestFixture, *redis.Client){
		"listed again": func(fixture *absentTestFixture, client *redis.Client) { listing10(fixture) },
		"mark moved": func(fixture *absentTestFixture, client *redis.Client) {
			markStrategy(t, client, "10", absentalerts.MarkDisabled, 5)
		},
		"mark gone": func(fixture *absentTestFixture, client *redis.Client) {
			client.HDel(context.Background(), departedKey, "10")
		},
	} {
		t.Run("before the close: "+name, func(t *testing.T) {
			fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
			client := withMarks(t, fixture)
			markStrategy(t, client, "10", absentalerts.MarkDisabled, 3)
			ctx := context.Background()
			fixture.loop.step(ctx)
			reads := 0
			fixture.loop.marks = marksHook{reader: fixture.loop.marks, before: func() {
				// The second read of the round is the re-check.
				if reads++; reads == 2 {
					enable(fixture, client)
				}
			}}
			fixture.now = fixture.now.Add(absentCloseInterval)
			fixture.loop.step(ctx)
			stats := fixture.loop.Stats()
			if len(fixture.link.closed()) != 0 || stats[absentalerts.OutcomeDepartedCloseDecided] != 1 {
				t.Fatalf("closed %+v stats %+v, want decided and then held back", fixture.link.closed(), stats)
			}
			want := absentalerts.OutcomeDepartedMarkChanged
			if name == "listed again" {
				want = absentalerts.OutcomeDepartedReappeared
			}
			if stats[want] != 1 {
				t.Fatalf("stats %+v, want %s counted", stats, want)
			}
		})
	}
}

type marksHook struct {
	reader departedMarkReader
	before func()
}

func (hook marksHook) DepartedMarks(ctx context.Context) (map[string]controlplane.DepartedMark, int, error) {
	hook.before()
	return hook.reader.DepartedMarks(ctx)
}

func (hook marksHook) DepartedMark(ctx context.Context, id string) (controlplane.DepartedMark, bool, error) {
	hook.before()
	return hook.reader.DepartedMark(ctx, id)
}

type failingMarks struct{}

func (failingMarks) DepartedMarks(context.Context) (map[string]controlplane.DepartedMark, int, error) {
	return nil, 0, errors.New("connection refused")
}

func (failingMarks) DepartedMark(context.Context, string) (controlplane.DepartedMark, bool, error) {
	return controlplane.DepartedMark{}, false, errors.New("connection refused")
}

// Marks that cannot be read leave every candidate to the implicit path,
// counted per round: the strategy is still closed, after its reads.
func TestUnreadableMarksLeaveTheImplicitPath(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	fixture.loop.marks = failingMarks{}
	fixture.mature(context.Background())
	stats := fixture.loop.Stats()
	if fixture.link.closed()["alert-1"] != 1 || stats[absentalerts.OutcomeCloseDecided] != 1 || stats[absentalerts.OutcomeMarksUnreadable] != 3 {
		t.Fatalf("stats %+v closed %+v", stats, fixture.link.closed())
	}
}

// testFence is a Control Leader lease the test took on the ownership store.
type testFence struct{ fence execution.OwnerFence }

func (f testFence) ControlFence() (execution.OwnerFence, bool) {
	return f.fence, f.fence.QueryGroup != ""
}

// A release restarts the leader. The candidates' reads are kept in the
// runtime Redis under the Control Leader's lease, so the next term closes on
// its second round - under a second observation of its own - on the reads
// the last term made - not after reading them
// all again, which is how a disabled strategy took 37 minutes - and
// never on its first. The old leader, its lease lapsed, writes nothing.
func TestTheNextTermClosesOnTheReadsTheLastOneKept(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	store, err := ownership.NewRedisStoreWithClient(client, "alarmd-test:ownership")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := store.AcquireControlLeader(ctx, "replica-a", time.Now(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	old := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	old.loop.memory = &redisAbsentMemory{store: store, fence: testFence{first.Fence}}
	old.loop.step(ctx)
	old.now = old.now.Add(absentCloseInterval)
	old.control.snapshot = liveSnapshot("observation-two", old.now, 100)
	old.loop.step(ctx)
	if len(old.link.closed()) != 0 {
		t.Fatal("closed before its reads")
	}

	// The release: replica-a's lease lapses and replica-b takes the term.
	var second ownership.PublicationAuthority
	testwait.Until(t, "replica-b takes the term once replica-a's lease lapses", func() bool {
		second, err = store.AcquireControlLeader(ctx, "replica-b", time.Now(), time.Minute)
		return err == nil
	})
	next := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	next.now = old.now
	next.control.snapshot = old.control.snapshot
	next.loop.memory = &redisAbsentMemory{store: store, fence: testFence{second.Fence}}
	next.now = next.now.Add(absentCloseInterval)
	next.loop.step(ctx)
	if len(next.link.closed()) != 0 || next.loop.Stats()[absentalerts.OutcomeWithinGrace] != 1 {
		t.Fatalf("the new term closed on its first round: %+v", next.loop.Stats())
	}
	next.now = next.now.Add(absentCloseInterval)
	next.control.snapshot = liveSnapshot("observation-three", next.now, 100)
	next.loop.step(ctx)
	if next.link.closed()["alert-1"] != 1 {
		t.Fatalf("the new term did not close on its second round, under its second observation, with the kept reads: %+v", next.loop.Stats())
	}

	// The old leader still thinks it leads; its save is refused, and it
	// sends nothing that round.
	kept, _ := store.ReadControlHash(ctx, ownership.ControlLeaderIdentity, absentMemoryNamespace)
	old.now = old.now.Add(absentCloseInterval)
	old.control.snapshot = liveSnapshot("observation-three", old.now, 100)
	old.loop.step(ctx)
	if len(old.link.closed()) != 0 || old.loop.Stats()[absentalerts.OutcomeMemoryFenced] != 1 {
		t.Fatalf("the old leader closed, or its refused save was not counted: %+v", old.loop.Stats())
	}
	after, _ := store.ReadControlHash(ctx, ownership.ControlLeaderIdentity, absentMemoryNamespace)
	if len(after) != len(kept) || after["10@system"] != kept["10@system"] {
		t.Fatalf("the old leader's write landed: %v -> %v", kept, after)
	}
}

// A mark that appears on a strategy already missing for rounds - the writer
// marked it late, or the strategy left for another reason and was then
// disabled - still waits one round under that mark before it is closed: the
// round in between is what lets it be enabled again.
func TestAMarkThatAppearsLateStillWaitsARound(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	client := withMarks(t, fixture)
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.loop.step(ctx)
	markStrategy(t, client, "10", absentalerts.MarkDisabled, 3)
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	if len(fixture.link.closed()) != 0 || fixture.loop.Stats()[absentalerts.OutcomeDepartedWaiting] != 1 {
		t.Fatalf("closed on the first round under a late mark: %+v", fixture.loop.Stats())
	}
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.loop.step(ctx)
	if fixture.link.closed()["alert-1"] != 1 || fixture.reason("alert-1") != "策略已被停用或删除，告警关闭" {
		t.Fatalf("not closed as disabled on the next round: %+v", fixture.loop.Stats())
	}
}

// A runtime Redis that cannot be reached at all: the store's own client
// against a port nothing listens on. Reading the memory fails, so the term
// starts from nothing, counted once; every save fails, counted, and the term
// goes on from its own memory - the strategy is closed after its reads, not
// held back by the store, and not closed early either.
func TestAnUnreachableMemoryStartsFromNothingAndTheTermGoesOn(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = dead.Close() })
	store, err := ownership.NewRedisStoreWithClient(dead, "alarmd-test:ownership")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	fixture.loop.memory = &redisAbsentMemory{store: store, fence: testFence{execution.OwnerFence{
		QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "replica-a", OwnerEpoch: 1, LeaseToken: "token"}}}
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.readAgain()
	if len(fixture.link.closed()) != 0 {
		t.Fatalf("closed before the term's own reads: %+v", fixture.loop.Stats())
	}
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	stats := fixture.loop.Stats()
	if fixture.link.closed()["alert-1"] != 1 || stats[absentalerts.OutcomeMemoryLoadFailed] != 1 ||
		stats[absentalerts.OutcomeMemorySaveFailed] != uint64(absentCloseMinReads) || stats[absentalerts.OutcomeMemoryFenced] != 0 {
		t.Fatalf("stats %+v closed %+v, want one failed load, a failed save a round, and the strategy closed on its reads",
			stats, fixture.link.closed())
	}
}

// Kept candidates that do not decode are skipped and counted, each: those
// candidates start over, and nothing is closed before this term read them
// missing on its own MinReads rounds.
func TestUndecodableKeptCandidatesStartOverCounted(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	store, err := ownership.NewRedisStoreWithClient(client, "alarmd-test:ownership")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lease, err := store.AcquireControlLeader(ctx, "replica-a", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FencedHashUpdate(ctx, ownership.FencedHashRequest{Fence: lease.Fence, Namespace: absentMemoryNamespace,
		Set: map[string][]byte{"10@system": []byte("not json"), "no-at-sign": []byte(`{"since":1,"reads":9}`)}}); err != nil {
		t.Fatal(err)
	}
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	fixture.loop.memory = &redisAbsentMemory{store: store, fence: testFence{lease.Fence}}
	fixture.loop.step(ctx)
	fixture.readAgain()
	if stats := fixture.loop.Stats(); len(fixture.link.closed()) != 0 || stats[absentalerts.OutcomeMemoryInvalid] != 2 || stats[absentalerts.OutcomeMemoryLoadFailed] != 0 {
		t.Fatalf("stats %+v closed %+v, want two undecodable fields counted as memory_invalid and nothing closed yet", stats, fixture.link.closed())
	}
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	if fixture.link.closed()["alert-1"] != 1 {
		t.Fatalf("not closed after the term's own reads: %+v", fixture.loop.Stats())
	}
}

// The re-check runs before each fast-path close, not once for the round:
// two strategies decided in one round, the second enabled again while the
// first's close is being sent, and the second is not closed.
func TestAStrategyEnabledAgainWhileAnEarlierCloseOfTheRoundRunsIsNotClosed(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-10", "10"), absentAlert("alert-11", "11")})
	client := withMarks(t, fixture)
	markStrategy(t, client, "10", absentalerts.MarkDisabled, 3)
	markStrategy(t, client, "11", absentalerts.MarkDisabled, 3)
	ctx := context.Background()
	fixture.loop.step(ctx)
	enabled := false
	fixture.link.onClose = func() {
		if enabled {
			return
		}
		enabled = true
		// Enabling 11 removes its mark and lists it again, as the writer
		// does in one transaction.
		client.HDel(context.Background(), departedKey, "11")
		fixture.control.snapshot.Strategies = append(fixture.control.snapshot.Strategies,
			controlplane.DepartedStrategy{TenantID: "system", StrategyID: "11", BusinessID: 2})
	}
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.loop.step(ctx)
	closed, stats := fixture.link.closed(), fixture.loop.Stats()
	if closed["alert-10"] != 1 || closed["alert-11"] != 0 || stats[absentalerts.OutcomeDepartedCloseDecided] != 2 ||
		stats[absentalerts.OutcomeDepartedReappeared] != 1 {
		t.Fatalf("closed %+v stats %+v, want 10 closed and 11, enabled again mid-round, held back", closed, stats)
	}
}

// A confirmation is not carried into a new term through the kept memory: a
// strategy confirmed missing by the last term that came back and left again
// while no leader watched, read by the new term under one observation, is
// not closed; a second observation of this term's own closes it.
func TestTheNewTermConfirmsAKeptCandidateItself(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	store, err := ownership.NewRedisStoreWithClient(client, "alarmd-test:ownership")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := store.AcquireControlLeader(ctx, "replica-a", time.Now(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	old := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	old.loop.memory = &redisAbsentMemory{store: store, fence: testFence{first.Fence}}
	old.loop.step(ctx)
	old.now = old.now.Add(absentCloseInterval)
	old.control.snapshot = liveSnapshot("observation-two", old.now, 100)
	old.loop.step(ctx)
	var second ownership.PublicationAuthority
	testwait.Until(t, "replica-b takes the term once replica-a's lease lapses", func() bool {
		second, err = store.AcquireControlLeader(ctx, "replica-b", time.Now(), time.Minute)
		return err == nil
	})
	next := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	next.loop.memory = &redisAbsentMemory{store: store, fence: testFence{second.Fence}}
	next.now = old.now
	for round := 0; round < 3; round++ {
		next.now = next.now.Add(absentCloseInterval)
		next.control.snapshot = liveSnapshot("observation-after-the-gap", next.now, 100)
		next.loop.step(ctx)
	}
	if len(next.link.closed()) != 0 || next.loop.Stats()[absentalerts.OutcomeUnconfirmed] == 0 {
		t.Fatalf("closed on the last term's confirmation: %+v", next.loop.Stats())
	}
	next.now = next.now.Add(absentCloseInterval)
	next.control.snapshot = liveSnapshot("observation-later", next.now, 100)
	next.loop.step(ctx)
	if next.link.closed()["alert-1"] != 1 {
		t.Fatalf("not closed once this term read a second observation: %+v", next.loop.Stats())
	}
}

// recheckFails reads the round's marks and fails every re-check's read.
type recheckFails struct{ departedMarkReader }

func (recheckFails) DepartedMark(context.Context, string) (controlplane.DepartedMark, bool, error) {
	return controlplane.DepartedMark{}, false, errors.New("connection reset")
}

// A re-check that cannot read its mark does not close, and marks_unreadable
// counts the round once however many re-checks of it failed.
func TestAReCheckThatCannotReadHoldsTheCloseCountedOnceARound(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-10", "10"), absentAlert("alert-11", "11")})
	client := withMarks(t, fixture)
	markStrategy(t, client, "10", absentalerts.MarkDeleted, 3)
	markStrategy(t, client, "11", absentalerts.MarkDeleted, 3)
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.loop.marks = recheckFails{fixture.loop.marks}
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.loop.step(ctx)
	stats := fixture.loop.Stats()
	if len(fixture.link.closed()) != 0 || stats[absentalerts.OutcomeDepartedCloseDecided] != 2 || stats[absentalerts.OutcomeMarksUnreadable] != 1 {
		t.Fatalf("closed %+v stats %+v, want both held back and marks_unreadable counted once", fixture.link.closed(), stats)
	}
}
