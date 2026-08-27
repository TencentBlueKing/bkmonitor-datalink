// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type Adapter struct {
	limits    contract.ReaderLimitsV2
	configErr error
}

type DecodeResult struct {
	Input           *EvaluationInput
	Terminals       TerminalSet
	RejectedReceipt *contract.MessageReceiptV1
	Rejected        bool
}

func New(limits contract.ReaderLimitsV2) *Adapter {
	return &Adapter{limits: limits, configErr: validateLimits(limits)}
}

func (adapter *Adapter) Validate() error {
	if adapter == nil {
		return errors.New("alarmd v2 adapter: adapter is nil")
	}
	return adapter.configErr
}

func (adapter *Adapter) Decode(ctx context.Context, payload []byte) (DecodeResult, error) {
	if err := adapter.Validate(); err != nil {
		return DecodeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return DecodeResult{}, err
	}
	framed, issues, err := contract.ReadExecutionEnvelopeV2(payload, adapter.limits)
	if err != nil {
		var framingError *contract.MessageFramingError
		if !errors.As(err, &framingError) {
			return DecodeResult{}, err
		}
		terminals := newTerminalSet([]Terminal{{
			Scope: ScopeMessage, ReasonCode: framingError.ReasonCode, FieldPath: framingError.FieldPath,
		}})
		var receipt *contract.MessageReceiptV1
		identity, identityErr := contract.ReadRejectedReceiptIdentityV2(payload, adapter.limits)
		if identityErr == nil {
			receipt, err = contract.BuildMessageReceiptV1(contract.MessageReceiptV1{
				ExecutionID: identity.ExecutionID, MessageID: identity.MessageID,
				PayloadDigest: identity.PayloadDigest, PlanSetDigest: identity.PlanSetDigest,
				SourceWindow: identity.SourceWindow, Status: contract.ReceiptStatusRejected,
				PerPlan:      []contract.PlanReceiptV1{},
				ReasonCounts: []contract.ReasonCountV1{{ReasonCode: framingError.ReasonCode, Count: 1}},
			})
			if err != nil {
				return DecodeResult{}, err
			}
		}
		return DecodeResult{Terminals: terminals, RejectedReceipt: receipt, Rejected: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return DecodeResult{}, err
	}
	isolation, err := isolateValidationIssues(&framed.Envelope, issues)
	if err != nil {
		return DecodeResult{}, err
	}

	envelope := framed.Envelope
	batch := newRecordBatch(envelope.Records, isolation.invalidRecords, isolation.invalidRecordFrom)
	planLimit := len(envelope.PlanSet.EvaluationPlans)
	if isolation.invalidPlanFrom != nil {
		planLimit = int(*isolation.invalidPlanFrom)
	}
	plans := make([]PlanView, 0, planLimit)
	selections := make([]PlanSelectionView, 0, planLimit)
	for index := 0; index < planLimit; index++ {
		ordinal := uint32(index)
		_, individuallyInvalid := isolation.invalidPlans[ordinal]
		selector, selectorErr := contract.NewSelectorIndexViewV2(envelope.Selectors[index].Selector, batch.Len())
		if selectorErr != nil {
			if individuallyInvalid {
				continue
			}
			return DecodeResult{}, selectorErr
		}
		if _, untrusted := isolation.untrustedPlanIDs[ordinal]; untrusted {
			continue
		}
		planID := envelope.PlanSet.EvaluationPlans[index].PlanID
		selections = append(selections, PlanSelectionView{
			ordinal: ordinal, planID: planID, evaluable: !individuallyInvalid, selector: selector, batch: batch,
		})
		if individuallyInvalid {
			continue
		}
		plan := envelope.PlanSet.EvaluationPlans[index]
		plans = append(plans, newPlanView(
			ordinal, plan, filterStrategyLevels(plan.StrategyIR, isolation.invalidLevels[ordinal]), selector, batch,
		))
	}
	terminals := newTerminalSet(isolation.terminals)
	route, ok := routeForCompleteness(envelope.QueryResult.Completeness)
	if !ok {
		return DecodeResult{}, errors.New("alarmd v2 adapter: unsupported completeness returned by contract Reader")
	}
	input := &EvaluationInput{
		mode: ModeQueryGroupV2, processingRoute: route,
		execution: ExecutionMetadata{
			ExecutionID: envelope.ExecutionID, MessageID: envelope.MessageID, TenantID: envelope.TenantID,
			QueryGroupKey: envelope.QueryGroup.Key, QueryMD5: envelope.QueryGroup.QueryMD5,
			QueryRevision: envelope.QueryGroup.QueryRevision, EvaluationTime: envelope.QueryGroup.EvaluationTime,
			SourceWindow: envelope.SourceWindow, Completeness: envelope.QueryResult.Completeness,
			QueryResultReason: envelope.QueryResult.ReasonCode, PlanSetDigest: envelope.PlanSet.PlanSetDigest,
			PayloadDigest: envelope.PayloadDigest,
		},
		dataset: newDatasetContractView(envelope.DatasetContract), recordBatch: batch,
		planViews: plans, planSelections: selections, terminals: terminals,
	}
	return DecodeResult{Input: input, Terminals: terminals}, nil
}

func validateLimits(limits contract.ReaderLimitsV2) error {
	values := []int{
		limits.MaxEnvelopeBytes, limits.MaxRecordsPerMessage, limits.MaxPlansPerMessage, limits.MaxLevelsPerPlan,
		limits.MaxSelectorBytes, limits.MaxRecordBytes, limits.MaxPlanSetBytes, limits.MaxContractDepth,
		limits.MaxStringBytes, limits.MaxValidationIssues,
	}
	for _, value := range values {
		if value <= 0 {
			return errors.New("alarmd v2 adapter: all Reader limits must be positive")
		}
	}
	return nil
}

func routeForCompleteness(completeness string) (ProcessingRoute, bool) {
	switch completeness {
	case contract.QueryCompletenessFull:
		return RouteFullPipeline, true
	case contract.QueryCompletenessPartial:
		return RouteDetectOnly, true
	case contract.QueryCompletenessUnavailable:
		return RouteNoEvaluation, true
	default:
		return "", false
	}
}

type validationIsolation struct {
	invalidPlans      map[uint32]struct{}
	untrustedPlanIDs  map[uint32]struct{}
	invalidPlanFrom   *uint32
	invalidLevels     map[uint32]map[uint32]struct{}
	invalidRecords    map[uint32]struct{}
	invalidRecordFrom *uint32
	terminals         []Terminal
}

func isolateValidationIssues(envelope *contract.ExecutionEnvelopeV2, issues []contract.ValidationIssue) (validationIsolation, error) {
	isolation := validationIsolation{terminals: make([]Terminal, 0, len(issues))}
	for _, issue := range issues {
		if issue.Scope == contract.ValidationScopePlan && issue.ReasonCode == contract.ReasonPlanInvalid && issue.PlanOrdinal != nil {
			if isolation.untrustedPlanIDs == nil {
				isolation.untrustedPlanIDs = make(map[uint32]struct{})
			}
			isolation.untrustedPlanIDs[*issue.PlanOrdinal] = struct{}{}
		}
	}
	for _, issue := range issues {
		if issue.ReasonCode == contract.ReasonValidationBudgetExceeded {
			if issue.UnverifiedTail == nil {
				return validationIsolation{}, fmt.Errorf("alarmd v2 adapter: validation budget issue has no unverified tail")
			}
			if err := isolation.addUnverifiedTail(envelope, issue); err != nil {
				return validationIsolation{}, err
			}
			continue
		}
		if err := isolation.addIssue(envelope, issue); err != nil {
			return validationIsolation{}, err
		}
	}
	return isolation, nil
}

func (isolation *validationIsolation) addIssue(envelope *contract.ExecutionEnvelopeV2, issue contract.ValidationIssue) error {
	terminal := Terminal{ReasonCode: issue.ReasonCode, FieldPath: issue.FieldPath}
	switch issue.Scope {
	case contract.ValidationScopePlan:
		terminal.Scope = ScopePlan
		ordinal, err := validOrdinal(issue.PlanOrdinal, len(envelope.PlanSet.EvaluationPlans), "plan")
		if err != nil {
			return err
		}
		terminal.PlanOrdinal = uint32Pointer(ordinal)
		if _, untrusted := isolation.untrustedPlanIDs[ordinal]; !untrusted {
			terminal.PlanID = envelope.PlanSet.EvaluationPlans[ordinal].PlanID
		}
		if isolation.invalidPlans == nil {
			isolation.invalidPlans = make(map[uint32]struct{})
		}
		isolation.invalidPlans[ordinal] = struct{}{}
	case contract.ValidationScopeLevel:
		terminal.Scope = ScopeLevel
		ordinal, err := validOrdinal(issue.PlanOrdinal, len(envelope.PlanSet.EvaluationPlans), "level plan")
		if err != nil || issue.LevelID == nil {
			if err != nil {
				return err
			}
			return errors.New("alarmd v2 adapter: level issue has no level identity")
		}
		levelID := *issue.LevelID
		terminal.PlanOrdinal = uint32Pointer(ordinal)
		if _, untrusted := isolation.untrustedPlanIDs[ordinal]; !untrusted {
			terminal.PlanID = envelope.PlanSet.EvaluationPlans[ordinal].PlanID
		}
		terminal.LevelID = uint32Pointer(levelID)
		if isolation.invalidLevels == nil {
			isolation.invalidLevels = make(map[uint32]map[uint32]struct{})
		}
		if isolation.invalidLevels[ordinal] == nil {
			isolation.invalidLevels[ordinal] = make(map[uint32]struct{})
		}
		isolation.invalidLevels[ordinal][levelID] = struct{}{}
	case contract.ValidationScopeRecord:
		terminal.Scope = ScopeRecord
		ordinal, err := validOrdinal(issue.RecordOrdinal, len(envelope.Records), "record")
		if err != nil {
			return err
		}
		terminal.RecordOrdinal = uint32Pointer(ordinal)
		if isolation.invalidRecords == nil {
			isolation.invalidRecords = make(map[uint32]struct{})
		}
		isolation.invalidRecords[ordinal] = struct{}{}
	default:
		return fmt.Errorf("alarmd v2 adapter: unsupported validation issue scope %q", issue.Scope)
	}
	isolation.terminals = append(isolation.terminals, terminal)
	return nil
}

func (isolation *validationIsolation) addUnverifiedTail(envelope *contract.ExecutionEnvelopeV2, issue contract.ValidationIssue) error {
	tail := issue.UnverifiedTail
	if tail.PlanFromOrdinal == nil && tail.RecordFromOrdinal == nil {
		return errors.New("alarmd v2 adapter: validation tail has no start ordinal")
	}
	terminal := Terminal{ReasonCode: contract.ReasonValidationBudgetExceeded, FieldPath: issue.FieldPath}
	if tail.PlanFromOrdinal != nil {
		start := *tail.PlanFromOrdinal
		if start > uint32(len(envelope.PlanSet.EvaluationPlans)) {
			return errors.New("alarmd v2 adapter: validation plan tail is outside Plan Set")
		}
		if isolation.invalidPlanFrom == nil || start < *isolation.invalidPlanFrom {
			isolation.invalidPlanFrom = uint32Pointer(start)
		}
		terminal.Scope = ScopePlan
		terminal.PlanFromOrdinal = uint32Pointer(start)
	}
	if tail.RecordFromOrdinal != nil {
		start := *tail.RecordFromOrdinal
		if start > uint32(len(envelope.Records)) {
			return errors.New("alarmd v2 adapter: validation record tail is outside RecordBatch")
		}
		if isolation.invalidRecordFrom == nil || start < *isolation.invalidRecordFrom {
			isolation.invalidRecordFrom = uint32Pointer(start)
		}
		if terminal.Scope == "" {
			terminal.Scope = ScopeRecord
		}
		terminal.RecordFromOrdinal = uint32Pointer(start)
	}
	isolation.terminals = append(isolation.terminals, terminal)
	return nil
}

func validOrdinal(pointer *uint32, count int, field string) (uint32, error) {
	if pointer == nil || *pointer >= uint32(count) {
		return 0, fmt.Errorf("alarmd v2 adapter: %s issue ordinal is outside input", field)
	}
	return *pointer, nil
}

func uint32Pointer(value uint32) *uint32 {
	return &value
}
