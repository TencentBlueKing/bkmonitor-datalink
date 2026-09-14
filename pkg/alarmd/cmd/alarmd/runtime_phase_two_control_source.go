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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// controlSourceState is what this process knows about the control plane's
// strategy source refresh, kept as state rather than as transitions. The
// bundle already reported the transition into the degraded state, once; a
// deployment whose source failed every round for three releases produced
// exactly one such report per release and read as healthy in between. The
// facts here are read at scrape and at publish, from the state as it is.
//
// Guarded by the bundle's mutex.
type controlSourceState struct {
	// role is the process's part in the refresh as of the last acquisition
	// attempt; unacquiredSince is when it last became unacquired, zero in
	// any other role.
	role            observability.ControlSourceRole
	unacquiredSince time.Time
	// known is set by the first control result this process applied, so
	// that before it there is no mode to report rather than a made-up one.
	known bool
	// succeededOnce records that this process saw a healthy control result.
	// It is one of the two facts that tell degraded_last_good from
	// never_succeeded; the other is the persisted success below.
	succeededOnce bool
	// degradedSince is when the current degraded episode of this process
	// began; zero while healthy. This process only: a restart resets it,
	// which is why the verdict reads it only where no persisted success
	// exists to read instead.
	degradedSince time.Time
	// lastFailureExit and lastFailure describe the last failed round: where
	// it stopped and what it said, bounded. They are the copy of the log
	// line that does not scroll away, cleared when a round succeeds.
	lastFailureExit string
	lastFailure     string
	// persistedSuccessAt is the last successful read of the persisted time
	// of the last successful refresh round, by any process; zero when none
	// is known. Read on every refresh tick by every replica, so the age it
	// yields is the same fact everywhere and survives every process.
	persistedSuccessAt time.Time
}

// controlSourceFailureTextLimit bounds the failure text the state keeps: it
// travels on every fleet snapshot, and the cause can quote the store.
const controlSourceFailureTextLimit = 256

// noteControlRole records the outcome of an acquisition attempt: acquired
// means leader; refused without error means another process holds the
// lease; an error means the process could not find out either way.
func (bundle *phaseTwoWorkerBundle) noteControlRole(acquired bool, err error) {
	role := observability.ControlSourceRoleUnacquired
	switch {
	case acquired:
		role = observability.ControlSourceRoleLeader
	case err == nil:
		role = observability.ControlSourceRoleFollower
	}
	bundle.mu.Lock()
	bundle.setControlRoleLocked(role)
	bundle.mu.Unlock()
}

func (bundle *phaseTwoWorkerBundle) setControlRoleLocked(role observability.ControlSourceRole) {
	state := &bundle.controlSource
	if role == observability.ControlSourceRoleUnacquired {
		if state.role != observability.ControlSourceRoleUnacquired || state.unacquiredSince.IsZero() {
			state.unacquiredSince = bundle.dependencies.Now()
		}
	} else {
		state.unacquiredSince = time.Time{}
	}
	state.role = role
}

// noteControlRoundLocked records the state a control result leaves this
// process in. Called under the bundle's mutex by whoever applies the result.
func (bundle *phaseTwoWorkerBundle) noteControlRoundLocked(result phaseTwoControlRefreshResult) {
	state := &bundle.controlSource
	state.known = true
	switch result.Status {
	case phaseTwoControlDegradedLastGood:
		if state.degradedSince.IsZero() {
			state.degradedSince = bundle.dependencies.Now()
		}
		state.lastFailureExit = string(controlplane.SourceRefreshExitOf(result.Cause))
		state.lastFailure = ""
		if result.Cause != nil {
			text := observability.SanitizeErrorText(result.Cause.Error())
			if len(text) > controlSourceFailureTextLimit {
				text = text[:controlSourceFailureTextLimit] + "..."
			}
			state.lastFailure = text
		}
	case phaseTwoControlHealthy:
		state.succeededOnce = true
		state.degradedSince = time.Time{}
		state.lastFailureExit = ""
		state.lastFailure = ""
	}
}

// readPersistedSourceSuccess refreshes the process's copy of the persisted
// success time. A read that fails leaves the previous copy standing: the
// age then reads older than it is, never younger.
func (bundle *phaseTwoWorkerBundle) readPersistedSourceSuccess(ctx context.Context) {
	if bundle == nil || bundle.dependencies.Control == nil || ctx.Err() != nil {
		return
	}
	at, known, err := bundle.dependencies.Control.SourceRefreshSuccessAt(ctx)
	if err != nil || !known {
		return
	}
	bundle.mu.Lock()
	bundle.controlSource.persistedSuccessAt = at
	bundle.mu.Unlock()
}

// controlSourceView is the state read out at one moment, for the metric
// collector and the fleet snapshot to render.
type controlSourceView struct {
	known           bool
	role            observability.ControlSourceRole
	mode            observability.ControlSourceMode
	lastSuccessAt   time.Time
	degradedSince   time.Time
	unacquiredSince time.Time
	lastFailureExit string
	lastFailure     string
	now             time.Time
}

func (bundle *phaseTwoWorkerBundle) controlSourceView() controlSourceView {
	bundle.mu.RLock()
	state := bundle.controlSource
	degraded := bundle.controlDegraded
	bundle.mu.RUnlock()
	view := controlSourceView{
		known: state.known, role: state.role, lastSuccessAt: state.persistedSuccessAt,
		degradedSince: state.degradedSince, unacquiredSince: state.unacquiredSince,
		lastFailureExit: state.lastFailureExit, lastFailure: state.lastFailure,
		now: bundle.dependencies.Now(),
	}
	if view.role == "" {
		view.role = observability.ControlSourceRoleUnacquired
	}
	switch {
	case !degraded:
		view.mode = observability.ControlSourceModeHealthy
	case state.succeededOnce || !state.persistedSuccessAt.IsZero():
		view.mode = observability.ControlSourceModeDegradedLastGood
	default:
		view.mode = observability.ControlSourceModeNeverSucceeded
	}
	return view
}

// staleBeyondBound is the verdict's one reading: no success for longer
// than the design accepts. From the persisted success where there is one;
// where there never was one, from how long this process has been failing,
// because a source broken from the first minute has nothing else to be
// measured by and is the case that most needs measuring.
func (view controlSourceView) staleBeyondBound() bool {
	switch {
	case !view.lastSuccessAt.IsZero():
		return view.now.Sub(view.lastSuccessAt) > controlplane.SourceStalenessBound
	case view.mode == observability.ControlSourceModeNeverSucceeded && !view.degradedSince.IsZero():
		return view.now.Sub(view.degradedSince) > controlplane.SourceStalenessBound
	default:
		return false
	}
}

func (view controlSourceView) leaderAbsentBeyondBound() bool {
	return view.role == observability.ControlSourceRoleUnacquired && !view.unacquiredSince.IsZero() &&
		view.now.Sub(view.unacquiredSince) > controlplane.SourceStalenessBound
}

// controlSourceStats is what the metric collector scrapes.
func (bundle *phaseTwoWorkerBundle) controlSourceStats() metric.ControlSourceStats {
	view := bundle.controlSourceView()
	return metric.ControlSourceStats{Known: view.known, Role: view.role, Mode: view.mode, LastSuccessAt: view.lastSuccessAt}
}

// controlSourceFleetFacts is what the fleet snapshot publishes. The ages
// are absent, not zero, where unknown: a zero would read as just now.
func (bundle *phaseTwoWorkerBundle) controlSourceFleetFacts() *fleet.ControlSourceFacts {
	view := bundle.controlSourceView()
	if !view.known {
		return nil
	}
	facts := &fleet.ControlSourceFacts{
		Role: string(view.role), Mode: string(view.mode),
		StaleBeyondBound: view.staleBeyondBound(), LeaderAbsentBeyondBound: view.leaderAbsentBeyondBound(),
		LastFailureExit: view.lastFailureExit, LastFailure: view.lastFailure,
	}
	if !view.lastSuccessAt.IsZero() {
		age := view.now.Sub(view.lastSuccessAt).Seconds()
		facts.LastSuccessAgeSeconds = &age
	}
	if !view.degradedSince.IsZero() {
		degraded := view.now.Sub(view.degradedSince).Seconds()
		facts.DegradedSecondsThisProcess = &degraded
	}
	return facts
}
