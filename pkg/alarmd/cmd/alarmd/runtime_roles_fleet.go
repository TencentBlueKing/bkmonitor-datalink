// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func wireRoleControlFacts(service *fleet.Service, registry *ownership.RedisStore, store *fleet.RedisStore, runtime *phaseTwoRoleRuntime) error {
	if runtime == nil {
		return nil
	}
	source, err := fleet.NewControlFactsSource(registry, store)
	if err != nil {
		return err
	}
	service.SetControlFactsSource(source)
	return nil
}

func publishRoleControlFacts(ctx context.Context, bundle *phaseTwoWorkerBundle, store *fleet.RedisStore, runtime *phaseTwoRoleRuntime) {
	if runtime == nil || bundle == nil || bundle.candidacy == "" || bundle.dependencies.ViewStreamStats == nil {
		return
	}
	stream := bundle.dependencies.ViewStreamStats()
	if !stream.Leading || stream.ControlEpoch == 0 {
		return
	}
	facts := fleet.ControlFacts{Replica: bundle.dependencies.Config.PhaseTwo.Worker.ID,
		Incarnation: runtime.Incarnation, ControlEpoch: stream.ControlEpoch, TakenAt: bundle.dependencies.Now(),
		Source: bundle.sourceFleetFacts(), Activation: bundle.activationFleetFacts(),
		ActivationHeader: bundle.activationHeaderFleetFacts(), ControlSource: bundle.controlSourceFleetFacts(),
		Rebalance: bundle.rebalanceFleetFacts(), AssignmentScope: bundle.assignmentScopeFleetFacts(),
		AssignmentSweep: bundle.assignmentSweepFleetFacts(), LeaderRound: bundle.leaderRoundFleetFacts(),
		ViewStream:     viewStreamFleetFacts(bundle.dependencies.ViewStreamStats, bundle.dependencies.Now)(),
		CandidacyClass: bundle.candidacy}
	err := store.PublishControl(ctx, facts)
	bundle.dependencies.Recorder.ObserveControlFactsWrite(fleet.ControlPublishResult(err))
	if err != nil {
		bundle.observe(ctx, "fleet", "control_facts", "failed", err)
	}
}
