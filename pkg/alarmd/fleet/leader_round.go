// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// The control leader's reconcile round is one serial pass: placements,
// moves, the index, the sweep of retired records, the view. How long the
// round takes was readable only as a whole, if at all, so "which part is
// the round spending its time in" -- the sweep's share, say -- had no
// reading. LeaderRoundFacts is the latest round stage by stage.

// The stages of a leader round, in the order the round runs them. Closed:
// the metric pre-creates one series per stage.
const (
	LeaderRoundStageAuthority        = "authority"
	LeaderRoundStageReadyWorkers     = "ready_workers"
	LeaderRoundStageContentScopes    = "content_scopes"
	LeaderRoundStageReconcileRecords = "reconcile_records"
	LeaderRoundStageByteMoves        = "byte_moves"
	LeaderRoundStageRebalanceMoves   = "rebalance_moves"
	LeaderRoundStageSplitDryRun      = "split_dry_run"
	LeaderRoundStageAssignmentIndex  = "assignment_index"
	LeaderRoundStageAssignmentSweep  = "assignment_sweep"
	LeaderRoundStageViewPublish      = "view_publish"
)

// LeaderRoundStages is every stage, in the order the round runs them.
var LeaderRoundStages = []string{
	LeaderRoundStageAuthority, LeaderRoundStageReadyWorkers, LeaderRoundStageContentScopes,
	LeaderRoundStageReconcileRecords, LeaderRoundStageByteMoves, LeaderRoundStageRebalanceMoves,
	LeaderRoundStageSplitDryRun, LeaderRoundStageAssignmentIndex, LeaderRoundStageAssignmentSweep,
	LeaderRoundStageViewPublish,
}

// The results of a leader round.
const (
	LeaderRoundCompleted = "completed"
	LeaderRoundFailed    = "failed"
)

// LeaderRoundFacts is one reconcile round of the control leader: when it
// started, whether it completed, and how long each stage it reached took.
// A failed round lists the stages it finished and names the one it failed
// in; the stages after it are absent, not zero.
type LeaderRoundFacts struct {
	At           time.Time          `json:"at"`
	Result       string             `json:"result"`
	FailedStage  string             `json:"failed_stage,omitempty"`
	TotalSeconds float64            `json:"total_seconds"`
	Stages       []LeaderRoundStage `json:"stages"`
}

// LeaderRoundStage is one stage of a round and its duration.
type LeaderRoundStage struct {
	Stage   string  `json:"stage"`
	Seconds float64 `json:"seconds"`
}
