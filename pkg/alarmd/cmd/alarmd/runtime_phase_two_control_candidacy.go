// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// Control Leader priority election (006, control-plane independence, section
// 2). Every process that can run the core control rounds -- lease,
// assignment, view, catalog activation, rebalancing -- stands for the lease in
// one of two classes, derived from its roles and never configured:
//
//   - preferred: a process with the control role. In the default all-roles
//     form that is every process, so nothing about that form changes except
//     that the lease is tried on every reconcile tick instead of every refresh
//     tick, and only by a process that has finished starting.
//   - fallback: a Worker without the control role. It carries the control
//     rounds dormant and takes the lease only when it is free and no
//     preferred process has a live, ready instance record. A live record is
//     one written within the record TTL, so a preferred process that stops
//     being heard from is replaced within that TTL plus one reconcile tick,
//     and one that only missed a write or two is not raced. A process that
//     stops rather than crashes says so in its record's last write, so it
//     holds nobody off. When the whole deployment rolls at once every
//     candidate is young: a young preferred process waits only for a
//     fallback that has been ready for a window, and a young fallback waits
//     for any ready preferred process until it has been ready for a window
//     itself, so the control process leads from the first epoch.
//
// A fallback Leader hands the lease back once a preferred process has been
// ready for a whole record TTL without a break: it finishes the round it is
// in, which has already published, and releases the lease; the preferred
// process takes it on its next reconcile tick, under the next epoch. The
// acquire script refuses anyone while the holder's deadline is live and the
// epoch fences everything the old holder still tries to write, so the lease
// never has two holders; between the release and the next tick it has none,
// for at most one reconcile interval.
//
// Nothing on the execution path reads any of this. Workers keep running the
// Query Groups they hold through a takeover and a handback alike; only who
// publishes the next Assignment and view changes.

// phaseTwoControlCandidacy is the class this process stands for the Control
// Leader lease in, or empty when it cannot hold it. A Worker without the
// control role is a fallback only when its configuration carries what the
// control rounds read: a Worker configured without a strategy source is
// still a valid Worker, and simply no candidate.
func phaseTwoControlCandidacy(cfg config.Config) string {
	switch {
	case cfg.HasRole(roles.Control):
		return ownership.ControlCandidacyPreferred
	case cfg.HasRole(roles.Worker) && cfg.DormantControlInputsError() == nil:
		return ownership.ControlCandidacyFallback
	}
	return ""
}

// controlCandidacyWindow is both T, how long a fallback waits after the
// last ready record of a preferred process before taking a free lease, and
// the stability window a preferred process must be ready for before a
// fallback Leader hands back. It is the instance record TTL: the longest a
// record can say "ready" without having been rewritten, and so the shortest
// time after which silence means the process is gone rather than slow.
func controlCandidacyWindow(cfg config.Config) time.Duration {
	return cfg.PhaseTwo.Worker.RegistrationTTL.Duration()
}

// controlHandbackMaxGap is how long a handback watch may go without a
// reading before its windows start again: two reconcile rounds, so one
// round that returned early is not a break but a run of them is.
func controlHandbackMaxGap(cfg config.Config) time.Duration {
	return 2 * cfg.PhaseTwo.Control.ReconcileInterval.Duration()
}

// controlContentionAllowed is whether a candidate tries the acquire script
// this round. leaseLive and preferredPresent are what a fallback reads
// before deciding; a preferred candidate does not need them, because the
// script itself refuses it while the lease is live, and it passes false.
func controlContentionAllowed(class string, ready, leaseLive, preferredPresent bool) bool {
	switch {
	case class == "" || !ready || leaseLive:
		return false
	case class == ownership.ControlCandidacyPreferred:
		return true
	case class == ownership.ControlCandidacyFallback:
		return !preferredPresent
	}
	return false
}

// preferredCandidatePresent says some preferred process other than this one
// has a record that is live at the instant, says it is ready, and has said
// so for a whole window. Only a stable preferred process holds a fallback
// off: one that keeps restarting is ready for a moment in every incarnation,
// and if each of those moments counted, a control process in a crash loop
// would leave a split deployment with no Leader at all -- the state the
// fallback exists for.
func preferredCandidatePresent(candidates []ownership.InstanceRegistration, self string, at time.Time, window time.Duration) bool {
	for _, candidate := range candidates {
		if candidate.InstanceID != self && candidate.CandidacyClass() == ownership.ControlCandidacyPreferred &&
			candidate.CandidacyStable(at, window) {
			return true
		}
	}
	return false
}

// readyPreferredPresent says some preferred process other than this one has
// a record that is live at the instant and says it is ready, however young.
// A fallback that is itself young defers to it: when the whole deployment
// rolls at once every candidate is young, and the control process should
// lead from the first epoch rather than after a fallback's term and a
// handback.
func readyPreferredPresent(candidates []ownership.InstanceRegistration, self string, at time.Time) bool {
	for _, candidate := range candidates {
		if candidate.InstanceID != self && candidate.CandidacyClass() == ownership.ControlCandidacyPreferred &&
			candidate.ControlCandidacyReady && candidate.ExpiresAt.After(at) {
			return true
		}
	}
	return false
}

// stableFallbackPresent says some fallback candidate other than this
// process has a record that is live at the instant and has said ready for a
// whole window. Only such a fallback is a reason for a young preferred
// process to wait: a fallback as young as it is would lead no longer and
// only cost a second epoch.
func stableFallbackPresent(candidates []ownership.InstanceRegistration, self string, at time.Time, window time.Duration) bool {
	for _, candidate := range candidates {
		if candidate.InstanceID != self && candidate.CandidacyClass() == ownership.ControlCandidacyFallback &&
			candidate.CandidacyStable(at, window) {
			return true
		}
	}
	return false
}

// preferredContentionAllowed is whether a preferred process takes a lease it
// found free. A stable one takes it at once. One that has not yet been ready
// for a whole window takes it only when no ready fallback is there to lead
// meanwhile: otherwise the fallback takes it, and hands it back once this
// process has been stable for the window -- the same hysteresis a handback
// has, so a process that keeps restarting never holds the lease long enough
// to drop it again. With no fallback at all, as in the default all-roles
// form, a preferred process takes a free lease at once, as it always did.
func preferredContentionAllowed(stable, leaseLive, fallbackReady bool) bool {
	return !leaseLive && (stable || !fallbackReady)
}

// controlHandbackWatch is what a fallback Leader remembers between rounds
// about the preferred processes it has seen ready: since when, without a
// break. A process is one incarnation, so a restart starts its window again,
// and any round that does not see it ready -- not ready, gone, or unreadable
// -- forgets it. Only the reconcile loop touches it.
//
// A round that returns before it reads the candidates (a refresh that
// failed, an assignment publish that failed, the degraded path) neither
// observes nor forgets; observedAt is when one last did, and a reading
// that comes more than the allowed gap after it starts every window again
// rather than counting rounds nobody read as unbroken readiness.
type controlHandbackWatch struct {
	readySince map[string]time.Time
	observedAt time.Time
}

func controlHandbackKey(instance ownership.InstanceRegistration) string {
	return instance.InstanceID + "\x00" + instance.Incarnation
}

// observe takes one round's reading of the preferred candidates and says
// whether one of them has now been ready for the whole window, and which.
func (watch *controlHandbackWatch) observe(candidates []ownership.InstanceRegistration, self string, at time.Time,
	window, maxGap time.Duration,
) (string, bool) {
	if !watch.observedAt.IsZero() && maxGap > 0 && at.Sub(watch.observedAt) > maxGap {
		watch.readySince = nil
	}
	watch.observedAt = at
	seen := make(map[string]time.Time, len(candidates))
	for _, candidate := range candidates {
		if candidate.InstanceID == self || candidate.CandidacyClass() != ownership.ControlCandidacyPreferred ||
			!candidate.ControlCandidacyReady || !candidate.ExpiresAt.After(at) {
			continue
		}
		key := controlHandbackKey(candidate)
		since, known := watch.readySince[key]
		if !known {
			since = at
		}
		seen[key] = since
	}
	watch.readySince = seen
	for _, candidate := range candidates {
		since, ready := seen[controlHandbackKey(candidate)]
		if ready && at.Sub(since) >= window {
			return candidate.InstanceID, true
		}
	}
	return "", false
}

// reset forgets every window: a round that could not read the candidates
// knows of no unbroken readiness, and a new term starts watching afresh.
func (watch *controlHandbackWatch) reset() {
	watch.readySince, watch.observedAt = nil, time.Time{}
}

// phaseTwoControlCandidateReader is what contending by class reads; the
// production ownership runtime passes its store's reads through.
type phaseTwoControlCandidateReader interface {
	ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error)
	ListPreferredControlCandidates(context.Context, time.Time) ([]ownership.InstanceRegistration, error)
	ListFallbackControlCandidates(context.Context, time.Time) ([]ownership.InstanceRegistration, error)
}

// phaseTwoHeldControlLeaderReleaser gives the lease up and says whether
// this call is what gave it up.
type phaseTwoHeldControlLeaderReleaser interface {
	ReleaseHeldControlLeader(context.Context) (bool, error)
}

// controlCandidacyReady is whether this process may contend now: it has
// finished starting and is not stopping.
func (bundle *phaseTwoWorkerBundle) controlCandidacyReady() bool {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return bundle.candidacyReady && !bundle.draining && !bundle.closed
}

// publishControlCandidacy hands the instance record writer this process's
// current candidacy readiness.
func (bundle *phaseTwoWorkerBundle) publishControlCandidacy() {
	if bundle.dependencies.Health != nil {
		bundle.dependencies.Health.candidacyReady.Store(bundle.candidacy != "" && bundle.controlCandidacyReady())
	}
}

// fallbackMayContend reads what a fallback needs before it tries the lease:
// whether a live lease is held and whether a live, ready preferred process
// is registered. The lease is read first, so a fallback in a led deployment
// spends one read per round and never lists the candidates.
func (bundle *phaseTwoWorkerBundle) fallbackMayContend(ctx context.Context) (bool, error) {
	reader, ok := bundle.dependencies.Ownership.(phaseTwoControlCandidateReader)
	if !ok {
		// A runtime that cannot tell whether a preferred process is there
		// does not contend as a fallback: that is the one guess that could
		// put a fallback in front of a preferred Leader.
		return false, nil
	}
	_, live, err := reader.ReadActiveControlLeader(ctx)
	if err != nil {
		return false, err
	}
	if live {
		return false, nil
	}
	at := bundle.dependencies.Now()
	candidates, err := reader.ListPreferredControlCandidates(ctx, at)
	if err != nil {
		return false, err
	}
	self, window := bundle.dependencies.Config.PhaseTwo.Worker.ID, controlCandidacyWindow(bundle.dependencies.Config)
	present := preferredCandidatePresent(candidates, self, at, window)
	if !present && !bundle.controlCandidacyStable(at) {
		// Until this fallback has itself been ready for a window, any ready
		// preferred process holds it off. The wait is bounded by that window,
		// so a preferred process in a crash loop still cannot starve a
		// deployment of a Leader.
		present = readyPreferredPresent(candidates, self, at)
	}
	return controlContentionAllowed(bundle.candidacy, true, false, present), nil
}

// controlCandidacyStable is whether this process's own record has said ready
// for a whole window, by the ready-since time its writer keeps.
func (bundle *phaseTwoWorkerBundle) controlCandidacyStable(at time.Time) bool {
	if bundle.dependencies.Health == nil || !bundle.controlCandidacyReady() {
		return false
	}
	since := bundle.dependencies.Health.candidacyReadySince.Load()
	return since != 0 && !at.Before(time.UnixMilli(since).Add(controlCandidacyWindow(bundle.dependencies.Config)))
}

// preferredMayContend is what a preferred process reads before it tries the
// lease when it is not yet stable: whether the lease is live, and if it is
// not, whether a ready fallback is there. A stable process reads neither and
// asks the acquire script, as before, so the steady state costs what it did.
func (bundle *phaseTwoWorkerBundle) preferredMayContend(ctx context.Context) (bool, error) {
	at := bundle.dependencies.Now()
	if bundle.controlCandidacyStable(at) {
		return true, nil
	}
	reader, ok := bundle.dependencies.Ownership.(phaseTwoControlCandidateReader)
	if !ok {
		// Nothing to wait for that this runtime could see: contend as a
		// preferred process always did.
		return true, nil
	}
	_, live, err := reader.ReadActiveControlLeader(ctx)
	if err != nil {
		return false, err
	}
	if live {
		return false, nil
	}
	fallbacks, err := reader.ListFallbackControlCandidates(ctx, at)
	if err != nil {
		return false, err
	}
	ready := stableFallbackPresent(fallbacks, bundle.dependencies.Config.PhaseTwo.Worker.ID, at,
		controlCandidacyWindow(bundle.dependencies.Config))
	return preferredContentionAllowed(false, false, ready), nil
}

// considerHandback is a fallback Leader's check, at the end of a round that
// has published, for a preferred process ready for the whole window. When
// there is one it gives the lease up now; when the candidates cannot be read
// it forgets every window, so a handback is never decided on a reading it
// did not make.
func (bundle *phaseTwoWorkerBundle) considerHandback(ctx context.Context) {
	reader, ok := bundle.dependencies.Ownership.(phaseTwoControlCandidateReader)
	if !ok {
		return
	}
	at := bundle.dependencies.Now()
	candidates, err := reader.ListPreferredControlCandidates(ctx, at)
	if err != nil {
		bundle.handback.reset()
		return
	}
	successor, due := bundle.handback.observe(candidates, bundle.dependencies.Config.PhaseTwo.Worker.ID, at,
		controlCandidacyWindow(bundle.dependencies.Config), controlHandbackMaxGap(bundle.dependencies.Config))
	if !due {
		return
	}
	bundle.handBackControlLeader(ctx, successor)
}

// handBackControlLeader steps this fallback Leader down and releases the
// lease. The lease renewal is stopped first, so nothing renews what is being
// given up; a renewal already in flight is refused by the release's fence
// and clears nothing that is still held.
func (bundle *phaseTwoWorkerBundle) handBackControlLeader(ctx context.Context, successor string) {
	releaser, ok := bundle.dependencies.Ownership.(phaseTwoHeldControlLeaderReleaser)
	if !ok {
		return
	}
	bundle.mu.Lock()
	cancel := bundle.cancelControl
	bundle.controlLeader = false
	bundle.setControlRoleLocked(observability.ControlSourceRoleFollower)
	bundle.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	bundle.handback.reset()
	released, err := releaser.ReleaseHeldControlLeader(ctx)
	if released {
		bundle.dependencies.Recorder.RecordControlLeaderTransition(ownership.ControlCandidacyPreferred, "handback")
	}
	if err != nil {
		// Renewal has stopped, so the lease runs out by its TTL instead and
		// the preferred process takes it then; the failure is said the way a
		// lost lease is.
		bundle.observe(ctx, observability.ComponentControlPlane, observability.StageSnapshotUnavailable,
			observability.ResultFailed, fmt.Errorf("hand the control lease back to %s: %w", successor, err))
	}
}

// recordControlTakeover counts the lease this process has just taken, by
// its class.
func (bundle *phaseTwoWorkerBundle) recordControlTakeover() {
	switch bundle.candidacy {
	case ownership.ControlCandidacyPreferred:
		bundle.dependencies.Recorder.RecordControlLeaderTransition(ownership.ControlCandidacyPreferred, "acquire_free")
	case ownership.ControlCandidacyFallback:
		bundle.dependencies.Recorder.RecordControlLeaderTransition(ownership.ControlCandidacyFallback, "fallback_takeover")
	}
}

// absentCloseWired is whether this process runs the deleted-strategy close
// loop: on every control-lease candidate, preferred or fallback, when the
// alert link's Console is configured. Closing the alerts of a strategy that
// no longer exists is alert correctness and belongs to the core control a
// fallback Leader carries; a fallback that led without it left those alerts
// open for as long as it led. The round itself does nothing on a process
// that does not hold the lease.
func absentCloseWired(cfg config.Config, consoleConfigured bool) bool {
	return consoleConfigured && phaseTwoControlCandidacy(cfg) != ""
}
