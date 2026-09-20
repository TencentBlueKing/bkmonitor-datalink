// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"errors"
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ErrGapGuardConflict is what every gap guard refusal unwraps to, so a caller
// can recognise the class without matching on text.
var ErrGapGuardConflict = errors.New("alarmd worker: activated Plan gap marker conflicts with the Slot")

// GapGuardProtection is one side of the comparison a gap guard refusal made:
// what is persisted for this Plan's ApplyVersion, or what this Slot proposes.
type GapGuardProtection struct {
	Kind              string
	ReasonCode        string
	Scopes            int
	RequiredFullSlots uint32
	MarkerRevision    uint64
	ObservedFullSlots uint32
}

func (protection GapGuardProtection) String() string {
	return fmt.Sprintf("kind=%s reason=%s scopes=%d required_full_slots=%d observed_full_slots=%d marker_revision=%d",
		emptyAsNone(protection.Kind), emptyAsNone(protection.ReasonCode), protection.Scopes,
		protection.RequiredFullSlots, protection.ObservedFullSlots, protection.MarkerRevision)
}

// GapGuardConflictError is a Slot refused because the gap marker already
// written for its ApplyVersion neither matches what this Slot would write nor
// already protects it.
//
// It carries both sides. A refusal that says only that there was a conflict
// leaves whoever reads it with two facts it cannot see and no way to tell
// which of them differs -- and this refusal repeats on every round for the
// same Query Group, so "look at it again" is not an answer either.
//
// Before it had a name the failure reached the page as internal_unknown, which
// is where a site that looked at a failure and could not classify it puts
// things. This one was classifiable all along.
type GapGuardConflictError struct {
	Plan            execution.PlanIdentity
	StateGeneration execution.StateGeneration
	ApplyVersion    execution.ApplyVersion
	Persisted       GapGuardProtection
	Proposed        GapGuardProtection
}

func (err *GapGuardConflictError) Error() string {
	var text strings.Builder
	text.WriteString(ErrGapGuardConflict.Error())
	fmt.Fprintf(&text, " [plan=%s/%s/%s generation=%s apply_epoch=%d]",
		err.Plan.TenantID, err.Plan.BusinessID, err.Plan.StrategyID,
		err.StateGeneration, err.ApplyVersion.StateApplyEpoch)
	fmt.Fprintf(&text, " persisted(%s) proposed(%s)", err.Persisted, err.Proposed)
	return text.String()
}

func (err *GapGuardConflictError) Unwrap() error { return ErrGapGuardConflict }

// ReasonCode is the bounded name this refusal reports as.
func (err *GapGuardConflictError) ReasonCode() execution.ReasonCode {
	return execution.ReasonCode(contract.ReasonGapGuardConflict)
}

// GapGuardConflictReason returns the bounded reason when err is a gap guard
// conflict, so an observer can name it instead of calling it unknown.
func GapGuardConflictReason(err error) (execution.ReasonCode, bool) {
	var conflict *GapGuardConflictError
	if !errors.As(err, &conflict) {
		return "", false
	}
	return conflict.ReasonCode(), true
}

func newGapGuardConflict(
	marker execution.GapGuardSnapshot,
	item execution.PlanGapLoadItem,
	mutation execution.PlanGapMutation,
	reason execution.ReasonCode,
	plan execution.ActivatedPlan,
) error {
	conflict := &GapGuardConflictError{
		Plan: item.Identity.Plan, StateGeneration: item.Identity.StateGeneration, ApplyVersion: item.ApplyVersion,
		Persisted: GapGuardProtection{
			ReasonCode: string(marker.ReasonCode), Scopes: len(marker.Scopes), MarkerRevision: marker.MarkerRevision,
		},
		Proposed: GapGuardProtection{
			ReasonCode: string(reason), Scopes: len(mutation.Scopes),
			RequiredFullSlots: plan.RequiredFullSlots, MarkerRevision: mutation.ExpectedMarkerRevision,
		},
	}
	conflict.Persisted.Kind = string(marker.Status)
	for _, scope := range marker.Scopes {
		// The Plan-wide scope is the one the comparison turns on; a level scope
		// protects one level and says nothing about the Plan.
		if scope.Scope.HasLevel {
			continue
		}
		conflict.Persisted.ReasonCode = string(scope.ReasonCode)
		conflict.Persisted.RequiredFullSlots = scope.RequiredFullSlots
		conflict.Persisted.ObservedFullSlots = scope.ObservedFullSlots
		break
	}
	for _, scope := range mutation.Scopes {
		if scope.Scope.HasLevel {
			continue
		}
		conflict.Proposed.Kind = string(scope.Kind)
		break
	}
	return conflict
}

func emptyAsNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}
