// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	// NoDataValueField names the synthetic series' only value: one for absent,
	// zero for present.
	//
	// The no-data level is compiled against a projection of its own rather than
	// the item's, and this field is why. Pointing its threshold at one of the
	// item's real value fields would make the detector declare it reads a
	// measurement while it actually receives the absence answer - a detector
	// whose declared input is a lie, which reads as correct in every log line
	// and every fingerprint it ever produces.
	NoDataValueField = "no_data"
	// noDataDataUnit is the identity unit. An absence answer is a count of
	// nothing, so there is no unit to convert and no prefix to apply.
	noDataDataUnit = "none"
	// noDataThresholdDecimal is the threshold: one absence is an anomaly, and
	// how many consecutive ones raise the alert is the trigger's business.
	noDataThresholdDecimal = "1"
	// noDataThresholdOperator makes a present group - value zero - not an
	// anomaly, which is what lets the recovery plan close the alert from the
	// same series the trigger opened it from.
	noDataThresholdOperator = "GTE"
)

// NoDataProjection is the input projection the no-data level is compiled
// against: one value field, and the dimensions the synthetic series carry.
//
// The tag is a dimension here because it is a dimension on the series. It is
// what keeps a no-data group's identity away from the real series of the same
// item, so leaving it out of the projection would describe a series that does
// not exist.
func NoDataProjection(config *contract.NoDataConfigV1) contract.InputProjectionV2 {
	if config == nil {
		return contract.InputProjectionV2{}
	}
	dimensions := make([]string, 0, len(config.AggDimension)+1)
	dimensions = append(dimensions, config.AggDimension...)
	dimensions = append(dimensions, contract.NoDataDimensionTag)
	// A projection's dimension list is a set, and the algorithm contract checks
	// that it is stated as one. The configured dimensions arrive deduplicated
	// but in the operator's order, so sorting here is what makes the tag's
	// position a property of its name rather than of where it was appended.
	sort.Strings(dimensions)
	return contract.InputProjectionV2{
		ValueFields:     []string{NoDataValueField},
		DimensionFields: dimensions,
		DataUnit:        noDataDataUnit,
	}
}

// BuildNoDataLevelIR turns a Plan's no_data configuration into the level the
// ordinary level compiler reads.
//
// Every number in it comes from the configuration and nothing is chosen here.
// The window and the required count are both Continuous, because the backend
// sets check_window_size and trigger_count from that one field; the step is the
// Plan's evaluation interval, which the trigger compiler requires them to be
// equal to, so there is one derivation of the period rather than two that agree
// until they do not.
//
// Building an IR and compiling it, rather than assembling a CompiledLevel by
// hand, is what makes the no-data level and a threshold level the same kind of
// object. The predicate digest, the detect fingerprint, the trigger
// fingerprint, the unit normalization and every refusal in the level compiler
// apply to it unchanged - and a hand-built level would be the one object in the
// system that none of that ever ran on.
//
// The level does not go into StrategyIR.Levels. It is a level the Plan detects
// on, not a level the strategy declares: adding it there would change the
// Plan's fingerprint, which re-keys the runtime state of every Plan on rollout,
// and would show up in the catalog as a level the operator never configured.
func BuildNoDataLevelIR(
	config *contract.NoDataConfigV1, semantics contract.ExecutionSemanticsV2,
) (contract.LevelIRV2, error) {
	if config == nil {
		return contract.LevelIRV2{}, errors.New("strategy: no-data level requires a no_data configuration")
	}
	if err := config.Validate(); err != nil {
		return contract.LevelIRV2{}, fmt.Errorf("strategy: no-data level: %w", err)
	}
	prefix := ""
	threshold, err := json.Marshal(thresholdConfigV1{
		ValueField:          NoDataValueField,
		DataUnit:            noDataDataUnit,
		ThresholdUnitPrefix: &prefix,
		Precision:           thresholdPrecision{DecimalPlaces: 6, Rounding: "HALF_EVEN"},
		Groups: []thresholdGroup{{Conditions: []thresholdCondition{
			{Operator: noDataThresholdOperator, ThresholdDecimal: noDataThresholdDecimal},
		}}},
	})
	if err != nil {
		return contract.LevelIRV2{}, fmt.Errorf("strategy: no-data threshold config: %w", err)
	}
	trigger, err := json.Marshal(triggerPlanConfigV1{
		WindowSize:        config.Continuous,
		RequiredAnomalies: config.Continuous,
		StepSeconds:       semantics.EvaluationInterval,
	})
	if err != nil {
		return contract.LevelIRV2{}, fmt.Errorf("strategy: no-data trigger config: %w", err)
	}
	enabled := true
	recovery, err := json.Marshal(struct {
		Enabled            *bool  `json:"enabled"`
		ConsecutiveWindows uint32 `json:"consecutive_windows"`
	}{Enabled: &enabled, ConsecutiveWindows: 1})
	if err != nil {
		return contract.LevelIRV2{}, fmt.Errorf("strategy: no-data recovery config: %w", err)
	}
	return contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: config.Level, Priority: config.Level},
		Connector:  contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{
			{Type: DetectorKindThreshold, Version: 1, Config: threshold},
		}},
		TriggerPlan:  contract.TypedPlanV1{Type: TriggerPlanTypeNOfM, Version: 1, Config: trigger},
		RecoveryPlan: contract.TypedPlanV1{Type: RecoveryPlanTypeContinuousTriggerMiss, Version: 1, Config: recovery},
	}, nil
}
