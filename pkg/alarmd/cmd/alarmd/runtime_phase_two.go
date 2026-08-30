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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var errPhaseTwoWorkerBundleNotAssembled = errors.New(
	"phase-two Go Access worker bundle is not assembled",
)

type phaseTwoApplicationDependencies struct {
	run func(context.Context, config.Config, *metric.Recorder, *observability.Logger) error
}

type runtimeModeDependencies struct {
	phaseOne applicationDependencies
	phaseTwo phaseTwoApplicationDependencies
}

func defaultPhaseTwoApplicationDependencies() phaseTwoApplicationDependencies {
	return phaseTwoApplicationDependencies{run: runPhaseTwoApplication}
}

// phaseTwoApplication is deliberately only a construction and health
// boundary. The final Worker Bundle must be supplied by the horizontal G1
// integration owner after real Snapshot, Assignment, lease and fencing are
// wired; this skeleton does not substitute fake production dependencies.
type phaseTwoApplication struct {
	health *phaseTwoApplicationHealth
}

func (a *phaseTwoApplication) HealthSnapshot() observability.HealthSnapshot {
	if a == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{PhaseTwo: true})
	}
	return a.health.HealthSnapshot()
}

func newPhaseTwoApplication(cfg config.Config) (*phaseTwoApplication, error) {
	if cfg.Input.Mode != config.InputModeGoAccess {
		return nil, fmt.Errorf("phase-two application requires input mode %q", config.InputModeGoAccess)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate phase-two application configuration: %w", err)
	}
	return &phaseTwoApplication{health: newPhaseTwoApplicationHealth()}, nil
}

func runPhaseTwoApplication(
	_ context.Context,
	cfg config.Config,
	_ *metric.Recorder,
	_ *observability.Logger,
) error {
	if _, err := newPhaseTwoApplication(cfg); err != nil {
		return err
	}
	return errPhaseTwoWorkerBundleNotAssembled
}

type phaseTwoReadiness struct {
	State             observability.HealthState
	Reasons           []observability.ReasonCode
	SnapshotReady     bool
	AssignmentReady   bool
	RuntimeStateReady bool
	OutputSinkReady   bool
	ResourceState     observability.ResourceState
}

type phaseTwoApplicationHealth struct {
	tracker *observability.HealthTracker
}

func newPhaseTwoApplicationHealth() *phaseTwoApplicationHealth {
	health := &phaseTwoApplicationHealth{tracker: observability.NewHealthTracker(observability.HealthSnapshot{})}
	health.Update(phaseTwoReadiness{State: observability.HealthStarting})
	return health
}

func (h *phaseTwoApplicationHealth) Update(readiness phaseTwoReadiness) {
	if h == nil || h.tracker == nil {
		return
	}
	h.tracker.Update(observability.HealthSnapshot{
		State: readiness.State, Reasons: append([]observability.ReasonCode(nil), readiness.Reasons...),
		ConfigLoaded: true, SchemaReady: true, PhaseTwo: true, SnapshotReady: readiness.SnapshotReady,
		AssignmentReady: readiness.AssignmentReady, RuntimeStateReady: readiness.RuntimeStateReady,
		OutputSinkReady: readiness.OutputSinkReady, ResourceState: readiness.ResourceState,
	})
}

func (h *phaseTwoApplicationHealth) HealthSnapshot() observability.HealthSnapshot {
	if h == nil || h.tracker == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{PhaseTwo: true})
	}
	snapshot := h.tracker.HealthSnapshot()
	// Kafka input claims and lag belong only to phase-one compatibility. Keep
	// them structurally absent from the phase-two readiness source.
	snapshot.AssignedClaims = 0
	snapshot.ConsumerLagRecords = 0
	snapshot.ConsumerLagKnown = false
	return observability.NormalizeHealthSnapshot(snapshot)
}
