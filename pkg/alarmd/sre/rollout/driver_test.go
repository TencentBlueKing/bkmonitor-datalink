// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// fakeCluster is one Deployment and a controller the test moves by hand.
// SetImage tests the image as the apiserver's JSON Patch test does.
type fakeCluster struct {
	mu     sync.Mutex
	dep    Deployment
	writes []imageWrite
	// refuseWrite fails every image write with this error.
	refuseWrite error
	// onGet, when set, changes the Deployment before each read.
	onGet func(*Deployment)
}

type imageWrite struct{ expected, image string }

func newFakeCluster() *fakeCluster {
	return &fakeCluster{dep: Deployment{UID: "uid-1", Generation: 1, ObservedGeneration: 1, Replicas: 1, StatusReplicas: 1, Updated: 1, Available: 1,
		ProgressDeadlineSeconds: 600, Strategy: "RollingUpdate", MaxSurge: 1, Containers: []Container{{Name: "app", Image: "app:1"}}}}
}

func (c *fakeCluster) GetDeployment(_ context.Context, _, _ string) (Deployment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onGet != nil {
		c.onGet(&c.dep)
	}
	if c.dep.UID == "" {
		return Deployment{}, ErrDeploymentGone
	}
	dep := c.dep
	dep.Containers = append([]Container(nil), c.dep.Containers...)
	return dep, nil
}

func (c *fakeCluster) SetImage(_ context.Context, _, _, uid string, index int, container, expected, image string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refuseWrite != nil {
		return 0, c.refuseWrite
	}
	if uid != c.dep.UID || index >= len(c.dep.Containers) || c.dep.Containers[index].Name != container || c.dep.Containers[index].Image != expected {
		return 0, ErrImageChanged
	}
	c.writes = append(c.writes, imageWrite{expected, image})
	c.dep.Containers[index].Image = image
	c.dep.Generation++
	// The controller starts the rollout: one new replica, not yet available.
	c.dep.Updated, c.dep.StatusReplicas, c.dep.Available = 0, 2, 1
	return c.dep.Generation, nil
}

func (c *fakeCluster) set(change func(*Deployment)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	change(&c.dep)
}

func (c *fakeCluster) imageWrites() []imageWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]imageWrite(nil), c.writes...)
}

// finish is the controller completing a rollout at the current generation.
func finish(dep *Deployment) {
	dep.ObservedGeneration, dep.Updated, dep.StatusReplicas, dep.Available = dep.Generation, dep.Replicas, dep.Replicas, dep.Replicas
}

// exceed is the controller giving up on a rollout at the current generation.
func exceed(dep *Deployment) {
	dep.ObservedGeneration = dep.Generation
	dep.Progressing.Status, dep.Progressing.Reason = "False", "ProgressDeadlineExceeded"
}

func testDriver(t *testing.T, store *Store, cluster Cluster, owner string) (*Driver, *finals) {
	t.Helper()
	ended := &finals{}
	d := NewDriver(DriverOptions{Store: store, Cluster: cluster, Owner: owner, Poll: 10 * time.Millisecond, Renew: 30 * time.Millisecond,
		Scan: 20 * time.Millisecond, Final: ended.add})
	d.margin = 0
	return d, ended
}

type finals struct {
	mu      sync.Mutex
	records []Record
}

func (f *finals) add(record Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, record)
}

func (f *finals) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.records)
}

// run runs the driver until the test ends; the stop it returns ends it and
// waits for every receipt it drove to be handed back.
func run(t *testing.T, d *Driver) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	testwait.Until(t, "the driver running on its context", func() bool { return d.runContext() == ctx })
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

// waitState waits for a receipt to reach a state, by reading the store.
func waitState(t *testing.T, store *Store, id, state string) Record {
	t.Helper()
	deadline := testwait.Deadline(t)
	for time.Now().Before(deadline) {
		if entry, err := store.Get(context.Background(), id); err == nil && entry.Record.State == state {
			return entry.Record
		}
		time.Sleep(5 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	entry, err := store.Get(context.Background(), id)
	t.Fatalf("receipt %s never reached %s: %+v %v", id, state, entry.Record, err)
	return Record{}
}

// waitStateIn waits for a receipt to reach any of the states, by reading the
// store: for a state the driver may pass through between two reads, name
// the states that follow it too.
func waitStateIn(t *testing.T, store *Store, id string, states ...string) Record {
	t.Helper()
	deadline := testwait.Deadline(t)
	for time.Now().Before(deadline) {
		if entry, err := store.Get(context.Background(), id); err == nil && slices.Contains(states, entry.Record.State) {
			return entry.Record
		}
		time.Sleep(5 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	entry, err := store.Get(context.Background(), id)
	t.Fatalf("receipt %s never reached any of %v: %+v %v", id, states, entry.Record, err)
	return Record{}
}

func request() Request {
	return Request{Namespace: "ns", Name: "app", UID: "uid-1", Container: "app", Image: "app:2", SessionID: "s-1"}
}

// The image change targets the Deployment the serving process belongs to:
// the receipt comes back at once, the serving process is then stopped and
// hands the receipt back, and another process takes it over in one scan
// and watches the rollout to its end. The image was written once, and the
// receipt names the image it replaced.
func TestASelfUpdateIsFinishedByTheProcessThatTakesItOver(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	serving, _ := testDriver(t, store, cluster, "pod-a/1")
	stopServing := run(t, serving)
	receipt, err := serving.Start(context.Background(), request())
	if err != nil || !receipt.Applied || receipt.State != StateRollingOut || receipt.Previous != "app:1" {
		t.Fatalf("receipt %+v %v, want rolling out from app:1", receipt, err)
	}
	stopServing()
	// The lease was handed back, not left to run out: a successor takes the
	// receipt well inside the lease.
	if entry, _ := store.Get(context.Background(), receipt.ID); !entry.LeaseExpired() {
		t.Fatalf("the stopped process kept its lease until %v (now %v)", entry.LeaseUntil, entry.Now)
	}
	successor, ended := testDriver(t, store, cluster, "pod-b/1")
	run(t, successor)
	waitDriving(t, successor, 1)
	cluster.set(finish)
	final := waitState(t, store, receipt.ID, StateComplete)
	// The final callback runs after the state is written: wait for it.
	waitFinals(t, ended, 1)
	if final.Previous != "app:1" || final.Image != "app:2" || len(cluster.imageWrites()) != 1 || ended.count() != 1 {
		t.Fatalf("final %+v, writes %v, finals %d", final, cluster.imageWrites(), ended.count())
	}
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.DrivenBy != "pod-b/1" {
		t.Fatalf("finished by %q, want the successor", entry.DrivenBy)
	}
}

// waitFinals waits for the final callback to have run n times.
func waitFinals(t *testing.T, f *finals, n int) {
	t.Helper()
	deadline := testwait.Deadline(t)
	for f.count() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
}

func waitDriving(t *testing.T, d *Driver, n int) {
	t.Helper()
	deadline := testwait.Deadline(t)
	for d.Driving() != n {
		if time.Now().After(deadline) {
			t.Fatalf("driving %d, want %d", d.Driving(), n)
		}
		time.Sleep(5 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
}

// A rollout the controller gives up on is reverted: the revert tests that
// the image is still the new one and writes the previous one back, and the
// receipt ends reverted once the revert's rollout completes.
func TestARolloutPastItsDeadlineIsReverted(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	run(t, d)
	receipt, err := d.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	cluster.set(exceed)
	waitWrites(t, cluster, 2)
	if writes := cluster.imageWrites(); writes[1] != (imageWrite{expected: "app:2", image: "app:1"}) {
		t.Fatalf("revert write %+v, want test app:2 replace app:1", writes[1])
	}
	cluster.set(func(dep *Deployment) {
		dep.Progressing.Status, dep.Progressing.Reason = "True", "NewReplicaSetAvailable"
		finish(dep)
	})
	final := waitState(t, store, receipt.ID, StateReverted)
	if !final.RevertSent {
		t.Fatalf("final %+v, want the revert recorded", final)
	}
}

func waitWrites(t *testing.T, cluster *fakeCluster, n int) {
	t.Helper()
	deadline := testwait.Deadline(t)
	for len(cluster.imageWrites()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("image writes %v, want %d", cluster.imageWrites(), n)
		}
		time.Sleep(5 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
}

// Someone else changing the image during the rollout is theirs to keep: no
// revert is written and the receipt ends superseded.
func TestAnImageChangedByAnotherHandIsLeftAlone(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(func(dep *Deployment) { dep.Containers[0].Image = "app:3"; exceed(dep) })
	waitState(t, store, receipt.ID, StateSuperseded)
	if writes := cluster.imageWrites(); len(writes) != 1 {
		t.Fatalf("writes %v, want only the change itself", writes)
	}
}

// Two processes taking over one receipt at once: one holds it, one revert
// is written, and the receipt ends reverted. Run under -race.
func TestOneProcessDrivesAReceiptTheOthersStandBy(t *testing.T) {
	store := newTestStore(t, 100*time.Millisecond)
	cluster := newFakeCluster()
	first, _ := testDriver(t, store, cluster, "pod-a/1")
	receipt, err := first.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	// The process that started it loses the receipt without handing it
	// back (its lease ends); its own next write is refused.
	if err := store.client.HSet(context.Background(), store.recordKey(receipt.ID), "lease", 0).Err(); err != nil {
		t.Fatal(err)
	}
	cluster.set(exceed)
	for _, owner := range []string{"pod-b/1", "pod-c/1", "pod-d/1"} {
		d, _ := testDriver(t, store, cluster, owner)
		run(t, d)
	}
	waitWrites(t, cluster, 2)
	cluster.set(func(dep *Deployment) { dep.Progressing.Status, dep.Progressing.Reason = "True", ""; finish(dep) })
	waitState(t, store, receipt.ID, StateReverted)
	reverts := 0
	for _, write := range cluster.imageWrites() {
		if write.image == "app:1" {
			reverts++
		}
	}
	if reverts != 1 {
		t.Fatalf("writes %v, want one revert", cluster.imageWrites())
	}
}

// A process that lost its receipt after writing the revert - paused, or cut
// off from Redis but not from the apiserver - leaves the image already the
// previous one. Its successor's revert finds that, watches the revert it
// did not write, and ends reverted rather than calling it someone else's.
func TestARevertAlreadyWrittenByALostHolderEndsReverted(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	record := testRecord("r1")
	record.Previous, record.Image, record.State, record.Generation = "app:1", "app:2", StateReverting, 2
	if err := store.Create(context.Background(), record, "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	// The lost holder's revert landed; its rollout is under way.
	cluster.set(func(dep *Deployment) { dep.Generation, dep.Updated, dep.StatusReplicas = 3, 0, 2 })
	_ = store.Release(context.Background(), "r1", "pod-a/1")
	d, _ := testDriver(t, store, cluster, "pod-b/1")
	run(t, d)
	waitState(t, store, "r1", StateReverting)
	testwait.Until(t, "the successor finding the revert already landed", func() bool {
		entry, err := store.Get(context.Background(), "r1")
		return err == nil && entry.Record.RevertSent
	})
	cluster.set(finish)
	final := waitState(t, store, "r1", StateReverted)
	if len(cluster.imageWrites()) != 0 || final.Generation != 3 {
		t.Fatalf("writes %v, generation %d: want no write and the landed revert's generation", cluster.imageWrites(), final.Generation)
	}
}

// The receipt is written before the image: a store that cannot take it
// refuses the change, and the Deployment is not written.
func TestNoReceiptNoImageChange(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	_ = store.client.Close()
	_, err := d.Start(context.Background(), request())
	var named *Error
	if !errors.As(err, &named) || named.Code != CodeStore || len(cluster.imageWrites()) != 0 {
		t.Fatalf("err %v writes %v, want refused before any write", err, cluster.imageWrites())
	}
}

// A Deployment that may stop every old Pod before a new one is ready, or
// one that is paused, is changed with a warning saying so.
func TestRiskySettingsWarnAndDoNotRefuse(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	cluster.set(func(dep *Deployment) { dep.Strategy, dep.Paused = "Recreate", true })
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	receipt, err := d.Start(context.Background(), request())
	if err != nil || !receipt.Applied || len(receipt.Warnings) != 2 || receipt.Warnings[0] != WarnPaused || receipt.Warnings[1] != WarnNoOldServing {
		t.Fatalf("receipt %+v %v, want applied with both warnings", receipt, err)
	}
}

// A revert that does not become ready either ends revert_failed.
func TestARevertThatFailsToo(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(exceed)
	waitWrites(t, cluster, 2)
	cluster.set(exceed)
	waitState(t, store, receipt.ID, StateRevertFailed)
}

// A process that stopped between writing the receipt and writing the image
// leaves a pending receipt. Its successor reads the image: written, the
// rollout is watched; not written, the receipt ends not_applied. A write
// the apiserver failed for another reason ends not_applied at once.
func TestAPendingReceiptIsDecidedByTheImage(t *testing.T) {
	// A watched rollout can pass rolling_out before any poll sees it, so the
	// written case accepts it or where a watched rollout goes next; the other
	// two are final.
	for _, tc := range []struct {
		name, image string
		states      []string
	}{
		{"written", "app:2", []string{StateRollingOut, StateComplete}},
		{"not written", "app:1", []string{StateNotApplied}},
		{"someone else's", "app:3", []string{StateSuperseded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t, LeaseDuration)
			cluster := newFakeCluster()
			cluster.set(func(dep *Deployment) { dep.Containers[0].Image = tc.image })
			if err := store.Create(context.Background(), testRecord("r1"), "pod-a/1"); err != nil {
				t.Fatal(err)
			}
			// The receipt is left pending by a released lease, which the
			// next driver takes at once; one still held would be taken only
			// once it ran out.
			if err := store.Release(context.Background(), "r1", "pod-a/1"); err != nil {
				t.Fatal(err)
			}
			d, _ := testDriver(t, store, cluster, "pod-b/1")
			run(t, d)
			waitStateIn(t, store, "r1", tc.states...)
		})
	}
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	cluster.refuseWrite = errors.New("apiserver unavailable")
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	receipt, err := d.Start(context.Background(), request())
	var named *Error
	if !errors.As(err, &named) || named.Code != CodeAPIError || receipt.State != StateNotApplied {
		t.Fatalf("receipt %+v err %v, want not_applied", receipt, err)
	}
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.State != StateNotApplied {
		t.Fatalf("stored %+v, want not_applied", entry.Record)
	}
}

// A slow rollout that keeps moving is not failed by the fallback bound,
// however long it takes in all: the bound counts from the last move.
func TestASlowRolloutThatKeepsMovingCompletes(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	cluster.set(func(dep *Deployment) { dep.ProgressDeadlineSeconds = 0; dep.Replicas = 20 })
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	d.margin = 150 * time.Millisecond
	run(t, d)
	receipt, err := d.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	// One replica every 50 ms: a second in all, against a 150 ms bound.
	for updated := int32(1); updated <= 20; updated++ {
		time.Sleep(50 * time.Millisecond) // fixed-wait: the controller's pace, one replica every 50 ms against the 150 ms bound, which the case sets
		cluster.set(func(dep *Deployment) { dep.Updated, dep.Available = updated, updated })
	}
	cluster.set(finish)
	waitState(t, store, receipt.ID, StateComplete)
	if writes := cluster.imageWrites(); len(writes) != 1 {
		t.Fatalf("writes %v, want no revert", writes)
	}
}

// A paused Deployment is never reverted by the fallback bound, however long
// it stays paused; the receipt keeps rolling out.
func TestAPausedRolloutIsNotReverted(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	cluster.set(func(dep *Deployment) { dep.ProgressDeadlineSeconds, dep.Paused = 0, true })
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	d.margin = 50 * time.Millisecond
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	time.Sleep(300 * time.Millisecond) // fixed-wait: a window in which the paused rollout must not be reverted; 300 ms is six times its 50 ms bound; an absence sends no signal to wait on
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.State != StateRollingOut || len(cluster.imageWrites()) != 1 {
		t.Fatalf("paused: %+v writes %v, want still rolling out", entry.Record, cluster.imageWrites())
	}
	// Resumed and still not moving: judged from the resume.
	cluster.set(func(dep *Deployment) { dep.Paused = false })
	waitWrites(t, cluster, 2)
}

// A receipt reads in one step by its id or by the Deployment's name: the
// image it replaced, where the rollout stands now, and who drives it. One
// in progress that nobody holds says its lease expired.
func TestAReceiptReadsInOneStep(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	receipt, err := d.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	for _, by := range []struct{ id, namespace, name string }{{receipt.ID, "", ""}, {"", "ns", "app"}} {
		view, err := d.Read(context.Background(), by.id, by.namespace, by.name)
		if err != nil || view.ID != receipt.ID || view.Previous != "app:1" || view.Live == nil || view.Live.CurrentImage != "app:2" ||
			view.Live.OldRemaining != 2 || view.DrivenBy != "pod-a/1" || view.LeaseExpired {
			t.Fatalf("read %+v: %+v (live %+v) %v", by, view, view.Live, err)
		}
	}
	if err := store.client.HSet(context.Background(), store.recordKey(receipt.ID), "lease", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if view, _ := d.Read(context.Background(), receipt.ID, "", ""); !view.LeaseExpired {
		t.Fatalf("an unheld receipt in progress reads %+v", view)
	}
	var named *Error
	if _, err := d.Read(context.Background(), "missing", "", ""); !errors.As(err, &named) || named.Code != CodeNotFound {
		t.Fatalf("a missing receipt = %v", err)
	}
}

// Every replica updated and available is not done while old Pods remain:
// the rollout completes only once they are gone.
func TestARolloutWithOldPodsLeftIsNotComplete(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(func(dep *Deployment) { finish(dep); dep.StatusReplicas = 2 })
	time.Sleep(100 * time.Millisecond) // fixed-wait: a window in which the rollout must not complete with an old Pod left; 100 ms is ten 10 ms polls; an absence sends no signal to wait on
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.State != StateRollingOut {
		t.Fatalf("with an old Pod left: %+v", entry.Record)
	}
	cluster.set(finish)
	waitState(t, store, receipt.ID, StateComplete)
}

// A ProgressDeadlineExceeded the controller wrote before it observed the
// change is about the previous rollout, not this one: nothing is reverted.
func TestADeadlineFromThePreviousGenerationIsNotThisRollouts(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	d.margin = time.Hour
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(func(dep *Deployment) {
		dep.ObservedGeneration = dep.Generation - 1
		dep.Progressing.Status, dep.Progressing.Reason = "False", "ProgressDeadlineExceeded"
	})
	time.Sleep(100 * time.Millisecond) // fixed-wait: a window in which the stale deadline must not revert the rollout; 100 ms is ten 10 ms polls; an absence sends no signal to wait on
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.State != StateRollingOut || len(cluster.imageWrites()) != 1 {
		t.Fatalf("a stale deadline: %+v writes %v", entry.Record, cluster.imageWrites())
	}
}

// A paused Deployment still carrying a deadline condition is not reverted.
func TestAPausedRolloutIsNotRevertedOnAStaleDeadline(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	d.margin = time.Hour
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(func(dep *Deployment) { exceed(dep); dep.Paused = true })
	time.Sleep(100 * time.Millisecond) // fixed-wait: a window in which the paused rollout must not be reverted on the stale deadline; 100 ms is ten 10 ms polls; an absence sends no signal to wait on
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.State != StateRollingOut || len(cluster.imageWrites()) != 1 {
		t.Fatalf("paused: %+v writes %v", entry.Record, cluster.imageWrites())
	}
}

// A process that lost its receipt does not write a revert: the receipt
// must say reverting first, and the store refuses that write to a process
// that no longer holds it.
func TestAProcessThatLostItsReceiptWritesNoRevert(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	// No renewal finds the loss first: only the write saying reverting can.
	d.renew = time.Hour
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	if err := store.client.HSet(context.Background(), store.recordKey(receipt.ID), "owner", "pod-b/1").Err(); err != nil {
		t.Fatal(err)
	}
	cluster.set(exceed)
	// Once it stops driving the receipt it can write nothing more: a revert
	// it was going to write is either among the writes by then or never.
	testwait.Until(t, "the process that lost the receipt stopping driving it", func() bool { return d.Driving() == 0 })
	if writes := cluster.imageWrites(); len(writes) != 1 {
		t.Fatalf("writes %v, want no revert from the process that lost the receipt", writes)
	}
}

// An image someone else wrote during the rollout is theirs even when that
// rollout then completes: the receipt ends superseded, not complete.
func TestAnotherHandsImageThatCompletesIsNotOurs(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(func(dep *Deployment) { dep.Containers[0].Image = "app:3"; dep.Generation++; finish(dep) })
	waitState(t, store, receipt.ID, StateSuperseded)
}

// hangingCluster's reads hang until their context ends, once hang is set.
type hangingCluster struct {
	*fakeCluster
	hang  atomic.Bool
	reads atomic.Int32
}

func (c *hangingCluster) GetDeployment(ctx context.Context, namespace, name string) (Deployment, error) {
	if c.hang.Load() {
		c.reads.Add(1)
		<-ctx.Done()
		return Deployment{}, ctx.Err()
	}
	return c.fakeCluster.GetDeployment(ctx, namespace, name)
}

// A hung apiserver read does not hold the driver past its lease: each read
// is bounded, the driver renews between them, and the receipt stays its own
// rather than lapsing to another process while it waits.
func TestAHungReadDoesNotHoldTheDriverPastItsLease(t *testing.T) {
	store := newTestStore(t, 300*time.Millisecond)
	cluster := &hangingCluster{fakeCluster: newFakeCluster()}
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	d.renew, d.callTimeout = 50*time.Millisecond, 30*time.Millisecond
	run(t, d)
	receipt, err := d.Start(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	cluster.hang.Store(true)
	// Each hung read is bounded, so the driver comes back to read again.
	testwait.Until(t, "the driver reading again past its hung reads", func() bool { return cluster.reads.Load() >= 3 })
	time.Sleep(700 * time.Millisecond) // fixed-wait: an absence window over two 300 ms leases; a driver that does not renew between its hung reads lets the lease lapse inside one
	entry, _ := store.Get(context.Background(), receipt.ID)
	if entry.DrivenBy != "pod-a/1" || entry.LeaseExpired() {
		t.Fatalf("after hung reads: driven by %q, lease expired %v, %d reads", entry.DrivenBy, entry.LeaseExpired(), cluster.reads.Load())
	}
	cluster.hang.Store(false)
	cluster.set(finish)
	waitState(t, store, receipt.ID, StateComplete)
}

// A pause is written when it starts and when it ends, not on every poll
// while it lasts: the stored record does not change during the pause.
func TestAPauseIsWrittenOnceNotEveryPoll(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	d.margin = time.Hour
	run(t, d)
	receipt, _ := d.Start(context.Background(), request())
	cluster.set(func(dep *Deployment) { dep.Paused = true })
	deadline := testwait.Deadline(t)
	var paused Record
	for time.Now().Before(deadline) {
		if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.Paused {
			paused = entry.Record
			break
		}
		time.Sleep(5 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	if !paused.Paused {
		t.Fatal("the pause was never written")
	}
	time.Sleep(150 * time.Millisecond) // fixed-wait: a window in which the paused record must not be rewritten; 150 ms is fifteen 10 ms polls; an absence sends no signal to wait on
	if entry, _ := store.Get(context.Background(), receipt.ID); entry.Record.LastProgressAt != paused.LastProgressAt {
		t.Fatalf("the record was rewritten during the pause: %d then %d", paused.LastProgressAt, entry.Record.LastProgressAt)
	}
	cluster.set(func(dep *Deployment) { dep.Paused = false })
	testwait.Until(t, "the resume being written", func() bool {
		entry, _ := store.Get(context.Background(), receipt.ID)
		return !entry.Record.Paused && entry.Record.LastProgressAt > paused.LastProgressAt
	})
}

// The apiserver refusing the service account is named, not an api_error.
func TestAForbiddenImageWriteIsNamed(t *testing.T) {
	store := newTestStore(t, LeaseDuration)
	cluster := newFakeCluster()
	cluster.refuseWrite = ErrForbidden
	d, _ := testDriver(t, store, cluster, "pod-a/1")
	_, err := d.Start(context.Background(), request())
	var named *Error
	if !errors.As(err, &named) || named.Code != CodeForbidden {
		t.Fatalf("a forbidden write = %v, want %s", err, CodeForbidden)
	}
}
