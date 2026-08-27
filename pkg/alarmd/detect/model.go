// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const (
	ExecutionModeStandard = "STANDARD"
	DetectionCoverageFull = "FULL"

	FactResultAnomalous   = "ANOMALOUS"
	FactResultNormal      = "NORMAL"
	FactResultUnavailable = "UNAVAILABLE"
	FactResultError       = "ERROR"
)

type ExecutionLimits struct {
	MaxPlans                  uint64
	MaxSelectedRecordsPerPlan uint64
	MaxSeriesPerPlan          uint64
	MaxRecordsPerSeries       uint64
	MaxLevelFacts             uint64
	MaxPredicateEvaluations   uint64
	MaxResultBytes            uint64
}

func (limits ExecutionLimits) valid() bool {
	return limits.MaxPlans > 0 && limits.MaxSelectedRecordsPerPlan > 0 && limits.MaxSeriesPerPlan > 0 &&
		limits.MaxRecordsPerSeries > 0 && limits.MaxLevelFacts > 0 && limits.MaxPredicateEvaluations > 0 &&
		limits.MaxResultBytes > 0
}

type EvaluateRequest struct {
	Completeness          string
	DatasetContractDigest string
	Plans                 []PlanExecution
	Limits                ExecutionLimits
}

type PlanExecution struct {
	View inputv2.PlanView
	Plan *strategy.CompiledPlan
}

type DetectionBatch struct {
	Completeness      string
	ExecutionMode     string
	DetectionCoverage string
	Series            []SeriesDetection
	Counts            DetectionCounts
}

type DetectionCounts struct {
	Plans                  uint64
	CompiledLevels         uint64
	SelectedRecords        uint64
	EvaluatedRecords       uint64
	Series                 uint64
	LevelFacts             uint64
	PredicateEvaluations   uint64
	AnomalousFacts         uint64
	NormalFacts            uint64
	UnavailableFacts       uint64
	ErrorFacts             uint64
	SkippedPlans           uint64
	SkippedSelectedRecords uint64
	EstimatedResultBytes   uint64
}

type SeriesDetection struct {
	PlanRef                 contract.RuntimePlanRefV1
	DimensionIdentityDigest string
	Records                 []RecordDetection
}

type RecordDetection struct {
	RecordOrdinal   uint32
	RecordID        string
	SourceTime      int64
	ProjectedValues []ProjectedValue
	LevelFacts      []LevelFact
}

type ProjectedValue struct {
	ValueRef         string
	NormalizerRef    string
	CanonicalDecimal string
	Available        bool
	ReasonCode       string
}

type LevelFact struct {
	Definition        contract.LevelDefinitionV2
	DetectFingerprint string
	Result            string
	ReasonCode        string
	Evidence          ThresholdEvidence
}

type ThresholdEvidence struct {
	PredicateDigest         string
	ProjectedValueOrdinal   uint32
	HasProjectedValue       bool
	MatchedAlgorithmOrdinal uint32
	HasMatchedAlgorithm     bool
	MatchedGroupOrdinal     uint32
	HasMatchedGroup         bool
	ResultReason            string
}
