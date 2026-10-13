// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Deployment is what a rollout reads of one Deployment.
type Deployment struct {
	UID                string
	Generation         int64
	ObservedGeneration int64
	// Replicas is spec.replicas; the status counts beside it.
	Replicas, StatusReplicas, Updated, Ready, Available int32
	Paused                                              bool
	// ProgressDeadlineSeconds is the Deployment's own, 600 when unset.
	ProgressDeadlineSeconds int32
	// Progressing is the Progressing condition: status, reason and when it
	// was last updated.
	Progressing struct{ Status, Reason, LastUpdate string }
	// Containers are the Pod template's containers, in order.
	Containers []Container
	// Strategy is spec.strategy.type; MaxSurge and MaxUnavailable resolved
	// against the replicas, as the controller rounds them.
	Strategy                 string
	MaxSurge, MaxUnavailable int32
}

// Container is one Pod template container.
type Container struct{ Name, Image string }

// image is a container's image and its index, -1 when absent.
func (d Deployment) image(container string) (string, int) {
	for index, c := range d.Containers {
		if c.Name == container {
			return c.Image, index
		}
	}
	return "", -1
}

// Cluster is the Kubernetes reads and writes a rollout makes.
type Cluster interface {
	GetDeployment(ctx context.Context, namespace, name string) (Deployment, error)
	// SetImage writes image to the container at index, in one patch that
	// first tests the Deployment's UID, the container's name and its image
	// being expected: ErrImageChanged when any of them is not. It returns
	// the generation written.
	SetImage(ctx context.Context, namespace, name, uid string, index int, container, expected, image string) (int64, error)
}

// ErrImageChanged is an image write the apiserver refused because the
// Deployment, the container or its image was not what the write expected.
var ErrImageChanged = errors.New("the deployment changed before the image write")

// ErrForbidden is a call the apiserver refused the ServiceAccount: the
// channel's role does not reach this Deployment.
var ErrForbidden = errors.New("the apiserver refused this deployment to the service account")

// ErrDeploymentGone is a Deployment that no longer exists.
var ErrDeploymentGone = errors.New("the deployment no longer exists")

// CallTimeout bounds each Kubernetes call the driver makes. It is shorter
// than the lease less a renewal, so a hung apiserver call cannot hold the
// loop past the lease: the driver renews between calls, and a process that
// lost the receipt finds out at its next write rather than acting on a
// record it no longer holds.
const CallTimeout = 5 * time.Second

// fallbackMargin is added to the progress deadline before the driver
// judges, by its own clock, a rollout that has not moved as failed: the
// controller's own judgment comes first.
const fallbackMargin = 30 * time.Second

// Driver watches receipts to their end. Each runs in its own goroutine
// while this process holds its lease; every state change is a write the
// store refuses once the lease is lost, and the goroutine then stops. Run
// scans for receipts whose lease ran out - their process stopped - and
// takes them over; a process stopping hands its receipts back at once.
type Driver struct {
	store   *Store
	cluster Cluster
	owner   string
	now     func() time.Time
	// poll, renew and scan are how often a rollout is read, a lease renewed
	// and the in-progress receipts scanned for one nobody holds.
	poll, renew, scan time.Duration
	// margin is fallbackMargin and callTimeout CallTimeout; tests shorten them.
	margin, callTimeout time.Duration
	// final hears each receipt that reached a final state here; revert each
	// revert written. Either may be nil.
	final  func(Record)
	revert func(Record)

	mu      sync.Mutex
	driving map[string]bool
	wg      sync.WaitGroup
	// running is Run's context: a receipt started by a request is driven
	// under it, not under the request, so it ends when the driver does.
	running context.Context
}

// DriverOptions configures a Driver; zero intervals take the defaults.
type DriverOptions struct {
	Store   *Store
	Cluster Cluster
	// Owner names this process: replica and incarnation.
	Owner             string
	Now               func() time.Time
	Poll, Renew, Scan time.Duration
	Final, Revert     func(Record)
}

// The default intervals.
const (
	DefaultPoll  = 2 * time.Second
	DefaultRenew = 5 * time.Second
	DefaultScan  = 10 * time.Second
)

// NewDriver builds a driver.
func NewDriver(options DriverOptions) *Driver {
	d := &Driver{store: options.Store, cluster: options.Cluster, owner: options.Owner, now: options.Now,
		poll: options.Poll, renew: options.Renew, scan: options.Scan, final: options.Final, revert: options.Revert,
		driving: map[string]bool{}, margin: fallbackMargin, callTimeout: CallTimeout}
	if d.now == nil {
		d.now = time.Now
	}
	if d.poll <= 0 {
		d.poll = DefaultPoll
	}
	if d.renew <= 0 {
		d.renew = DefaultRenew
	}
	if d.scan <= 0 {
		d.scan = DefaultScan
	}
	return d
}

// Driving is how many receipts this process is watching.
func (d *Driver) Driving() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.driving)
}

// Run scans for receipts nobody holds until ctx ends, then waits for every
// receipt this process drives to hand its lease back.
func (d *Driver) Run(ctx context.Context) {
	d.mu.Lock()
	d.running = ctx
	d.mu.Unlock()
	ticker := time.NewTicker(d.scan)
	defer ticker.Stop()
	for {
		d.takeOver(ctx)
		select {
		case <-ctx.Done():
			d.wg.Wait()
			return
		case <-ticker.C:
		}
	}
}

func (d *Driver) takeOver(ctx context.Context) {
	ids, err := d.store.Active(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if d.isDriving(id) {
			continue
		}
		record, taken, err := d.store.Take(ctx, id, d.owner)
		if err != nil || !taken {
			continue
		}
		d.Adopt(ctx, record)
	}
}

// runContext is Run's context, or a background one before Run starts.
func (d *Driver) runContext() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running == nil {
		return context.Background()
	}
	return d.running
}

func (d *Driver) isDriving(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.driving[id]
}

// Adopt drives a receipt this process holds until it ends, the lease is
// lost, or ctx ends - when the lease is handed back.
func (d *Driver) Adopt(ctx context.Context, record Record) {
	d.mu.Lock()
	if d.driving[record.ID] {
		d.mu.Unlock()
		return
	}
	d.driving[record.ID] = true
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.driving, record.ID)
			d.mu.Unlock()
		}()
		d.drive(ctx, record)
	}()
}

func (d *Driver) drive(ctx context.Context, record Record) {
	// Stopping hands the receipt back for a successor to take at once,
	// whatever step the stop interrupted - a write on the ended context
	// fails too - on a context of its own, since this one has ended.
	defer func() {
		if ctx.Err() != nil {
			release, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = d.store.Release(release, record.ID, d.owner)
			cancel()
		}
	}()
	poll := time.NewTicker(d.poll)
	defer poll.Stop()
	renewed := d.now()
	for {
		next, changed := d.step(ctx, record)
		switch {
		case changed:
			if err := d.store.Put(ctx, next, d.owner); err != nil {
				return
			}
			record, renewed = next, d.now()
			if Final(record.State) {
				if d.final != nil {
					d.final(record)
				}
				return
			}
		case d.now().Sub(renewed) >= d.renew:
			if err := d.store.Renew(ctx, record.ID, d.owner); err != nil {
				return
			}
			renewed = d.now()
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		}
	}
}

// step reads the Deployment and returns the record as it now stands, and
// whether it changed. A revert is written here, after the record says it is
// reverting: a write the store refuses stops this process before it
// touches the Deployment.
// getDeployment and setImage are the cluster calls, each bounded by
// callTimeout.
func (d *Driver) getDeployment(ctx context.Context, namespace, name string) (Deployment, error) {
	ctx, cancel := context.WithTimeout(ctx, d.callTimeout)
	defer cancel()
	return d.cluster.GetDeployment(ctx, namespace, name)
}

func (d *Driver) setImage(ctx context.Context, record Record, index int, expected, image string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, d.callTimeout)
	defer cancel()
	return d.cluster.SetImage(ctx, record.Namespace, record.Name, record.UID, index, record.Container, expected, image)
}

func (d *Driver) step(ctx context.Context, record Record) (Record, bool) {
	now := d.now()
	dep, err := d.getDeployment(ctx, record.Namespace, record.Name)
	if errors.Is(err, ErrDeploymentGone) {
		return end(record, StateTargetChanged, "the deployment no longer exists", now), true
	}
	if err != nil {
		// Unread: nothing is judged this poll, and the time without
		// progress keeps counting.
		return record, false
	}
	if dep.UID != record.UID {
		return end(record, StateTargetChanged, "the deployment was replaced", now), true
	}
	image, _ := dep.image(record.Container)
	switch record.State {
	case StatePending:
		// The process that wrote the receipt stopped around its write: the
		// image says whether the write landed.
		switch image {
		case record.Image:
			record.State, record.Generation, record.LastProgressAt = StateRollingOut, dep.Generation, now.UnixMilli()
			return record, true
		case record.Previous:
			return end(record, StateNotApplied, "the image write did not land", now), true
		default:
			return end(record, StateSuperseded, "the image was changed by someone else", now), true
		}
	case StateRollingOut:
		if image != record.Image {
			return end(record, StateSuperseded, "the image was changed by someone else", now), true
		}
		if complete(dep, record.Generation) {
			return end(record, StateComplete, "", now), true
		}
		record, moved := progressed(record, dep, now)
		if failed(record, dep, now, d.margin) {
			record.State, record.RevertSent = StateReverting, false
			return record, true
		}
		return record, moved
	case StateReverting:
		if !record.RevertSent {
			return d.sendRevert(ctx, record, dep, now)
		}
		if image != record.Previous {
			return end(record, StateSuperseded, "the image was changed by someone else during the revert", now), true
		}
		if complete(dep, record.Generation) {
			return end(record, StateReverted, "", now), true
		}
		record, moved := progressed(record, dep, now)
		if failed(record, dep, now, d.margin) {
			return end(record, StateRevertFailed, "the reverted image did not become ready either", now), true
		}
		return record, moved
	}
	return record, false
}

// sendRevert writes the previous image back. The record already says
// reverting (written before this). A refused write is read by what the
// image is now: the previous one means a revert already landed - one sent
// by a process that lost the receipt after sending it - and is watched; ours
// still in place means a write raced and is tried once more; anything else
// is someone else's image, left as it is.
func (d *Driver) sendRevert(ctx context.Context, record Record, dep Deployment, now time.Time) (Record, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		image, index := dep.image(record.Container)
		switch {
		case image == record.Previous:
			record.RevertSent, record.Generation, record.LastProgressAt = true, dep.Generation, now.UnixMilli()
			return record, true
		case image != record.Image || index < 0:
			return end(record, StateSuperseded, "the image was changed by someone else before the revert", now), true
		}
		generation, err := d.setImage(ctx, record, index, record.Image, record.Previous)
		if err == nil {
			record.RevertSent, record.Generation, record.LastProgressAt = true, generation, now.UnixMilli()
			if d.revert != nil {
				d.revert(record)
			}
			return record, true
		}
		if !errors.Is(err, ErrImageChanged) {
			// Not written; the next poll tries again.
			return record, false
		}
		if dep, err = d.getDeployment(ctx, record.Namespace, record.Name); err != nil {
			return record, false
		}
		if dep.UID != record.UID {
			return end(record, StateTargetChanged, "the deployment was replaced", now), true
		}
	}
	return record, false
}

// complete is a rollout finished at generation: observed, every replica
// updated and available, and none of the old ones left.
func complete(dep Deployment, generation int64) bool {
	return dep.ObservedGeneration >= generation && dep.Updated == dep.Replicas && dep.StatusReplicas == dep.Replicas &&
		dep.Available == dep.Replicas
}

// progressed notes a move: an updated or available replica more, or the
// Progressing condition updated. A paused Deployment does not move and is
// not judged, so its time without progress restarts when it resumes.
func progressed(record Record, dep Deployment, now time.Time) (Record, bool) {
	seen := Progress{Updated: dep.Updated, Available: dep.Available, Progressing: dep.Progressing.LastUpdate}
	moved := seen != record.Progress
	toggled := dep.Paused != record.Paused
	if moved || toggled || dep.Paused {
		// While paused, the time without progress restarts at every poll in
		// memory, and the record is written only when the pause starts or
		// ends (or the rollout moves): the bound counts from the resume.
		record.Progress, record.LastProgressAt, record.Paused = seen, now.UnixMilli(), dep.Paused
	}
	return record, moved || toggled
}

// failed is a rollout that will not finish: the controller says its
// progress deadline passed at the generation watched, or, the controller
// saying nothing, it has not moved for the deadline and a margin since it
// last did. A paused Deployment is never failed.
func failed(record Record, dep Deployment, now time.Time, margin time.Duration) bool {
	if dep.Paused {
		return false
	}
	if dep.ObservedGeneration >= record.Generation && dep.Progressing.Status == "False" && dep.Progressing.Reason == "ProgressDeadlineExceeded" {
		return true
	}
	deadline := time.Duration(dep.ProgressDeadlineSeconds)*time.Second + margin
	return record.LastProgressAt > 0 && now.Sub(time.UnixMilli(record.LastProgressAt)) > deadline
}

func end(record Record, state, detail string, now time.Time) Record {
	record.State, record.Detail, record.EndedAt = state, detail, now.UnixMilli()
	return record
}
