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
	"bytes"
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const ModeQueryGroupV2 = "QUERY_GROUP_V2"

type ProcessingRoute string

const (
	RouteFullPipeline ProcessingRoute = "FULL_PIPELINE"
	RouteDetectOnly   ProcessingRoute = "DETECT_ONLY"
	RouteNoEvaluation ProcessingRoute = "NO_EVALUATION"
)

type ExecutionMetadata struct {
	ExecutionID       string
	MessageID         string
	TenantID          string
	QueryGroupKey     string
	QueryMD5          string
	QueryRevision     string
	EvaluationTime    int64
	SourceWindow      contract.SourceWindowV2
	Completeness      string
	QueryResultReason string
	PlanSetDigest     string
	PayloadDigest     string
}

type DatasetContractView struct {
	schemaDigest        string
	normalizationDigest string
	identityFields      []string
	sourceTimeField     string
	collectionTimeField string
	receivedTimeField   string
}

func newDatasetContractView(source contract.DatasetContractV2) DatasetContractView {
	return DatasetContractView{
		schemaDigest: source.SchemaDigest, normalizationDigest: source.NormalizationDigest,
		identityFields: append([]string(nil), source.IdentityFields...), sourceTimeField: source.SourceTimeField,
		collectionTimeField: source.CollectionTimeField, receivedTimeField: source.ReceivedTimeField,
	}
}

func (view DatasetContractView) SchemaDigest() string        { return view.schemaDigest }
func (view DatasetContractView) NormalizationDigest() string { return view.normalizationDigest }
func (view DatasetContractView) IdentityFields() []string {
	return append([]string(nil), view.identityFields...)
}
func (view DatasetContractView) SourceTimeField() string     { return view.sourceTimeField }
func (view DatasetContractView) CollectionTimeField() string { return view.collectionTimeField }
func (view DatasetContractView) ReceivedTimeField() string   { return view.receivedTimeField }

type RecordBatch struct {
	records     []contract.CanonicalRecordV2
	invalid     map[uint32]struct{}
	invalidFrom *uint32
	validLen    int
}

func newRecordBatch(records []contract.CanonicalRecordV2, invalid map[uint32]struct{}, invalidFrom *uint32) *RecordBatch {
	validLimit := len(records)
	if invalidFrom != nil {
		validLimit = int(*invalidFrom)
	}
	invalidCount := 0
	for ordinal := range invalid {
		if int(ordinal) < validLimit {
			invalidCount++
		}
	}
	return &RecordBatch{records: records, invalid: invalid, invalidFrom: invalidFrom, validLen: validLimit - invalidCount}
}

func (batch *RecordBatch) Len() int {
	if batch == nil {
		return 0
	}
	return len(batch.records)
}

func (batch *RecordBatch) ValidLen() int {
	if batch == nil {
		return 0
	}
	return batch.validLen
}

func (batch *RecordBatch) Record(index int) (RecordView, bool) {
	if batch == nil || index < 0 || index >= len(batch.records) {
		return RecordView{}, false
	}
	ordinal := uint32(index)
	if _, invalid := batch.invalid[ordinal]; invalid || batch.invalidFrom != nil && ordinal >= *batch.invalidFrom {
		return RecordView{}, false
	}
	return RecordView{record: &batch.records[index]}, true
}

type RecordView struct {
	record *contract.CanonicalRecordV2
}

func (view RecordView) RecordID() string {
	if view.record == nil {
		return ""
	}
	return view.record.RecordID
}

func (view RecordView) SourceTime() int64 {
	if view.record == nil {
		return 0
	}
	return view.record.SourceTime
}

func (view RecordView) BusinessID() string {
	if view.record == nil {
		return ""
	}
	return view.record.BusinessID
}

func (view RecordView) DimensionIdentityDigest() string {
	if view.record == nil {
		return ""
	}
	return view.record.DimensionIdentity.Digest
}

func (view RecordView) CollectionTime() (int64, bool) {
	if view.record == nil || view.record.CollectionTime == nil {
		return 0, false
	}
	return *view.record.CollectionTime, true
}

func (view RecordView) ReceivedTime() int64 {
	if view.record == nil {
		return 0
	}
	return view.record.ReceivedTime
}

func (view RecordView) Value(name string) (json.RawMessage, bool) {
	if view.record == nil {
		return nil, false
	}
	value, ok := view.record.Values[name]
	return bytes.Clone(value), ok
}

func (view RecordView) Dimension(name string) (json.RawMessage, bool) {
	if view.record == nil {
		return nil, false
	}
	value, ok := view.record.Dimensions[name]
	return bytes.Clone(value), ok
}

// Dimensions materializes a copy only for consumers that need the complete
// dimension set, such as final event construction.
func (view RecordView) Dimensions() map[string]json.RawMessage {
	if view.record == nil {
		return nil
	}
	dimensions := make(map[string]json.RawMessage, len(view.record.Dimensions))
	for name, value := range view.record.Dimensions {
		dimensions[name] = bytes.Clone(value)
	}
	return dimensions
}

type StrategyIRView struct {
	strategy contract.StrategyIRV2
}

func (view StrategyIRView) Snapshot() contract.StrategyIRV2 {
	return cloneStrategyIR(view.strategy)
}

// PlanSelectionView exists for every Plan whose selector is trustworthy,
// including a Plan terminalized before Evaluation. M7 uses it to keep Receipt
// selected/terminal counts conservative without exposing StrategyIR.
type PlanSelectionView struct {
	ordinal   uint32
	planID    string
	evaluable bool
	selector  contract.SelectorIndexViewV2
	batch     *RecordBatch
}

func (view PlanSelectionView) PlanOrdinal() uint32 { return view.ordinal }
func (view PlanSelectionView) PlanID() string      { return view.planID }
func (view PlanSelectionView) Evaluable() bool     { return view.evaluable }
func (view PlanSelectionView) SelectedCount() int  { return view.selector.Len() }

func (view PlanSelectionView) ForEachSelectedSlot(visitor func(recordOrdinal uint32, record RecordView, valid bool) error) error {
	return forEachSelectedSlot(view.selector, view.batch, visitor)
}

type PlanView struct {
	ordinal  uint32
	planID   string
	strategy StrategyIRView
	selector contract.SelectorIndexViewV2
	batch    *RecordBatch
}

func (view PlanView) PlanOrdinal() uint32      { return view.ordinal }
func (view PlanView) PlanID() string           { return view.planID }
func (view PlanView) Strategy() StrategyIRView { return view.strategy }
func (view PlanView) SelectedCount() int       { return view.selector.Len() }

// ForEachSelectedSlot preserves selector membership for both valid and
// terminal record slots without materializing an ordinal slice.
func (view PlanView) ForEachSelectedSlot(visitor func(recordOrdinal uint32, record RecordView, valid bool) error) error {
	return forEachSelectedSlot(view.selector, view.batch, visitor)
}

func forEachSelectedSlot(
	selector contract.SelectorIndexViewV2,
	batch *RecordBatch,
	visitor func(recordOrdinal uint32, record RecordView, valid bool) error,
) error {
	var visitErr error
	selector.ForEach(func(recordIndex uint32) bool {
		record, ok := batch.Record(int(recordIndex))
		if err := visitor(recordIndex, record, ok); err != nil {
			visitErr = err
			return false
		}
		return true
	})
	return visitErr
}

func (view PlanView) ForEachRecord(visitor func(RecordView) error) error {
	return view.ForEachSelectedSlot(func(_ uint32, record RecordView, valid bool) error {
		if !valid {
			return nil
		}
		return visitor(record)
	})
}

type EvaluationInput struct {
	mode            string
	processingRoute ProcessingRoute
	execution       ExecutionMetadata
	dataset         DatasetContractView
	recordBatch     *RecordBatch
	planViews       []PlanView
	planSelections  []PlanSelectionView
	terminals       TerminalSet
}

func (input *EvaluationInput) ProcessingRoute() ProcessingRoute {
	if input == nil {
		return ""
	}
	return input.processingRoute
}

func (input *EvaluationInput) Mode() string {
	if input == nil {
		return ""
	}
	return input.mode
}

func (input *EvaluationInput) Execution() ExecutionMetadata {
	if input == nil {
		return ExecutionMetadata{}
	}
	return input.execution
}

func (input *EvaluationInput) DatasetContract() DatasetContractView {
	if input == nil {
		return DatasetContractView{}
	}
	return input.dataset
}

func (input *EvaluationInput) RecordBatch() *RecordBatch {
	if input == nil {
		return nil
	}
	return input.recordBatch
}

func (input *EvaluationInput) PlanViews() []PlanView {
	if input == nil {
		return nil
	}
	return append([]PlanView(nil), input.planViews...)
}

func (input *EvaluationInput) PlanSelections() []PlanSelectionView {
	if input == nil {
		return nil
	}
	return append([]PlanSelectionView(nil), input.planSelections...)
}

func (input *EvaluationInput) Terminals() TerminalSet {
	if input == nil {
		return TerminalSet{}
	}
	return input.terminals
}

func cloneStrategyIR(source contract.StrategyIRV2) contract.StrategyIRV2 {
	cloned := source
	cloned.RequiredFeatures = append([]string(nil), source.RequiredFeatures...)
	cloned.InputProjection = cloneProjection(source.InputProjection)
	cloned.Levels = make([]contract.LevelIRV2, len(source.Levels))
	for index := range source.Levels {
		cloned.Levels[index] = source.Levels[index]
		cloned.Levels[index].DetectPlan.Algorithms = make([]contract.AlgorithmIRV2, len(source.Levels[index].DetectPlan.Algorithms))
		for algorithmIndex := range source.Levels[index].DetectPlan.Algorithms {
			cloned.Levels[index].DetectPlan.Algorithms[algorithmIndex] = source.Levels[index].DetectPlan.Algorithms[algorithmIndex]
			cloned.Levels[index].DetectPlan.Algorithms[algorithmIndex].Config = bytes.Clone(source.Levels[index].DetectPlan.Algorithms[algorithmIndex].Config)
		}
		cloned.Levels[index].TriggerPlan.Config = bytes.Clone(source.Levels[index].TriggerPlan.Config)
		cloned.Levels[index].RecoveryPlan.Config = bytes.Clone(source.Levels[index].RecoveryPlan.Config)
	}
	return cloned
}

func filterStrategyLevels(source contract.StrategyIRV2, invalid map[uint32]struct{}) contract.StrategyIRV2 {
	if len(invalid) == 0 {
		return source
	}
	filtered := source
	filtered.Levels = make([]contract.LevelIRV2, 0, len(source.Levels))
	for _, level := range source.Levels {
		if _, found := invalid[level.Definition.LevelID]; !found {
			filtered.Levels = append(filtered.Levels, level)
		}
	}
	return filtered
}

func cloneProjection(source contract.InputProjectionV2) contract.InputProjectionV2 {
	cloned := source
	cloned.ValueFields = append([]string(nil), source.ValueFields...)
	cloned.DimensionFields = append([]string(nil), source.DimensionFields...)
	return cloned
}
