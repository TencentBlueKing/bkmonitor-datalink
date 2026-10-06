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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// activationStanding is what this process knows about bringing the fleet's
// activation to the publication the source produced, kept as state rather
// than as transitions, for the same reason controlSourceState is.
//
// It exists because the source clock cannot see this failure. A refresh
// round publishes, then activates; when activation fails every round, the
// source's persisted success time keeps advancing -- every round did publish
// -- while the fleet keeps executing the last good activation. On a running
// deployment that went on for half a day: the activation error sat on every
// fleet snapshot as control_source.last_failure, the verdict read only the
// source clock, and no line on the page said the fleet was executing content
// that was no longer the current publication.
//
// Guarded by the bundle's mutex. Only the control leader attempts activation,
// so only the leader's standing is ever non-empty.
type activationStanding struct {
	// attempted is set by the first round that tried, so that before it
	// there is no standing to report rather than a made-up one.
	attempted bool
	// published is the publication the last attempt tried to reach; applied
	// is the one the activation was on after it. Equal after a success.
	published controlplane.SnapshotPublicationRef
	applied   controlplane.SnapshotPublicationRef
	// lastSuccessAt is the last attempt that succeeded, this process only;
	// failingSince is when the current run of failures began, zero while
	// succeeding; consecutiveFailures is how long that run is.
	lastSuccessAt       time.Time
	failingSince        time.Time
	consecutiveFailures int
	// failureStage, failureClass and lastFailure describe the last failed
	// attempt: the bounded classification the activation_failed log line
	// carries, and the text, bounded. Cleared by a success.
	failureStage string
	failureClass string
	lastFailure  string
}

// noteActivationLocked records one round's activation outcome. Called under
// the bundle's mutex by whoever applies the control result. A round that
// did not attempt activation leaves the standing as it was.
func (bundle *phaseTwoWorkerBundle) noteActivationLocked(outcome *phaseTwoActivationOutcome) {
	if outcome == nil {
		return
	}
	state := &bundle.activation
	now := bundle.dependencies.Now()
	state.attempted = true
	state.published = outcome.Published
	// Zero means the last good activation could not even be read; the
	// previous applied value is kept, because "unknown now" is not evidence
	// that the fleet stopped executing what it was executing.
	if outcome.Applied != (controlplane.SnapshotPublicationRef{}) {
		state.applied = outcome.Applied
	}
	if outcome.Cause == nil {
		state.lastSuccessAt = now
		state.failingSince = time.Time{}
		state.consecutiveFailures = 0
		state.failureStage, state.failureClass, state.lastFailure = "", "", ""
		return
	}
	if state.failingSince.IsZero() {
		state.failingSince = now
	}
	state.consecutiveFailures++
	state.failureStage, state.failureClass = "", ""
	if outcome.Failure != nil {
		state.failureStage = string(outcome.Failure.Stage)
		state.failureClass = string(outcome.Failure.Class)
	}
	text := observability.SanitizeErrorText(outcome.Cause.Error())
	if len(text) > controlSourceFailureTextLimit {
		text = text[:controlSourceFailureTextLimit] + "..."
	}
	state.lastFailure = text
}

// activationFleetFacts is what the fleet snapshot publishes. Nil before any
// attempt, which every replica but the leader is. Behind is the one fact the
// verdict reads once it has held past the bound: the fleet is executing a
// publication that is not the current one.
func (bundle *phaseTwoWorkerBundle) activationFleetFacts() *fleet.ActivationFacts {
	bundle.mu.RLock()
	state := bundle.activation
	bundle.mu.RUnlock()
	if !state.attempted {
		return nil
	}
	now := bundle.dependencies.Now()
	facts := &fleet.ActivationFacts{
		Applied: string(state.applied.SnapshotRevision), AppliedEpoch: state.applied.PublicationEpoch,
		Published: string(state.published.SnapshotRevision), PublishedEpoch: state.published.PublicationEpoch,
		Behind:              state.published != state.applied,
		ConsecutiveFailures: state.consecutiveFailures,
		FailureStage:        state.failureStage, FailureClass: state.failureClass, LastFailure: state.lastFailure,
	}
	if !state.lastSuccessAt.IsZero() {
		age := now.Sub(state.lastSuccessAt).Seconds()
		facts.LastSuccessAgeSeconds = &age
	}
	if bundle.dependencies.ActivationBlocked != nil {
		reading := bundle.dependencies.ActivationBlocked()
		for _, count := range reading.ByReason {
			facts.BlockedQueryGroups += count
		}
		if facts.BlockedQueryGroups > 0 {
			reasons := make([]string, 0, len(reading.ByReason))
			for reason, count := range reading.ByReason {
				reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
			}
			sort.Strings(reasons)
			facts.BlockedReasons = strings.Join(reasons, ",")
			facts.BlockedSamples = strings.Join(reading.Samples, ",")
		}
	}
	if !state.failingSince.IsZero() {
		failing := now.Sub(state.failingSince).Seconds()
		facts.FailingSecondsThisProcess = &failing
		// The same bound the source is held to: a publication the design
		// accepts may be this stale, so an activation this far behind it is
		// past the exposure the design accepts, whatever the reason.
		facts.BehindBeyondBound = facts.Behind && now.Sub(state.failingSince) > controlplane.SourceStalenessBound
	}
	return facts
}
