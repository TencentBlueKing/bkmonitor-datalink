// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// detectionSnapshot is what an event's detection decided on
// (contract.DetectionSnapshotV1), from what the decision already holds:
//
//   - ref_values, on an ABNORMAL event of a record that was read - not a
//     no-data point - the named outputs aligned to the record, each in its
//     state, with its value when SUCCESS or PARTIAL; the legacy output's is
//     the record's own value;
//   - each level the event decided - ABNORMAL levels on an ABNORMAL event,
//     RECOVERY levels on a RECOVERY one - with its algorithms in order, the
//     facts of the one that decided, its trigger window, the start of the
//     window's anomalies, and the unit its facts are in.
//
// No detection is done again. The algorithm that decided gives its
// normalized value, and a Threshold also its matched OR group, by ordinal,
// with that group's conditions; the facts the other algorithms need - their
// references, sides and history values - are not held at the decision and
// are not in this version's snapshot.
func detectionSnapshot(request EvaluationRequestV2, event *contract.TriggerEventV1) contract.DetectionSnapshotV1 {
	snapshot := contract.DetectionSnapshotV1{Version: contract.DetectionSnapshotVersion, Levels: []contract.DetectionLevelV1{}}
	_, noData := request.RecordRef.Dimensions[contract.NoDataDimensionTag]
	if event.EventKind == contract.TriggerEventAbnormal && !noData {
		snapshot.RefValues = refValues(request.NamedOutputs, request.Record.RecordID, currentValue(event.Observed))
	}
	levels := make([]contract.LevelResultV1, 0, len(event.LevelResults))
	for _, result := range event.LevelResults {
		if result.Result == event.EventKind {
			levels = append(levels, result)
		}
	}
	sort.SliceStable(levels, func(left, right int) bool { return levels[left].LevelID < levels[right].LevelID })
	if len(levels) > contract.MaxDetectionSnapshotLevels {
		levels = levels[:contract.MaxDetectionSnapshotLevels]
	}
	for _, result := range levels {
		snapshot.Levels = append(snapshot.Levels, snapshotLevel(request.Plan, result))
	}
	return snapshot
}

// encodeDetectionSnapshot is the encoder snapshots are held to their bounds
// by; a test replaces it to make one fail.
var encodeDetectionSnapshot = contract.EncodeDetectionSnapshotV1

// DetectionSnapshotFacts is what one event's detection snapshot came to:
// its result (contract.DetectionSnapshotResults), and the state of each
// named output a snapshot the event carries holds - none on one that
// carries no ref_values, or that was dropped.
type DetectionSnapshotFacts struct {
	Result string
	States []string
}

func snapshotFacts(snapshot contract.DetectionSnapshotV1, truncated bool) *DetectionSnapshotFacts {
	facts := &DetectionSnapshotFacts{Result: contract.DetectionSnapshotWhole}
	if truncated {
		facts.Result = contract.DetectionSnapshotTruncated
	}
	for _, value := range snapshot.RefValues {
		facts.States = append(facts.States, value.State)
	}
	sort.Strings(facts.States)
	return facts
}

// currentValue is the event's current value, as both outputs write it: the
// observed value named value, or the one observed value there is. The two
// agree: every observed record carries its value under the dataset's
// CanonicalValueField, "value" (controlplane's legacy query compiler).
func currentValue(observed contract.TriggerObservedV1) json.RawMessage {
	if value, found := observed.Values["value"]; found {
		return value
	}
	if len(observed.Values) == 1 {
		for _, value := range observed.Values {
			return value
		}
	}
	return nil
}

// refValues is the named outputs at one record, by reference name; nil
// when the record is not in the table. The legacy output's value, where it
// has one, is the event's current value, by assignment: the contract
// promises the two equal, and the snapshot does not lean on the table
// having read the same field of the same record.
func refValues(table *execution.NamedOutputRecords, recordID string, current json.RawMessage) map[string]contract.DetectionRefValueV1 {
	if table == nil {
		return nil
	}
	values, found := table.Records[recordID]
	if !found || len(values) != len(table.Refs) {
		return nil
	}
	refs := make(map[string]contract.DetectionRefValueV1, len(values))
	for index, value := range values {
		entry := contract.DetectionRefValueV1{State: string(value.State)}
		if value.State == execution.NamedOutputSuccess || value.State == execution.NamedOutputPartial {
			entry.Value = value.Value
			if table.Refs[index] == table.LegacyRef && current != nil {
				entry.Value = current
			}
		}
		refs[table.Refs[index]] = entry
	}
	return refs
}

func snapshotLevel(plan *strategy.CompiledPlan, result contract.LevelResultV1) contract.DetectionLevelV1 {
	trigger := result.DecisionWindow.Trigger
	level := contract.DetectionLevelV1{Level: result.LevelID, Algorithms: []contract.DetectionAlgorithmV1{},
		Window:           contract.DetectionWindowV1{Required: trigger.RequiredAnomalies, Observed: trigger.ObservedAnomalies, Size: trigger.WindowSize},
		AnomalyBeginTime: trigger.AnomalyBeginTime}
	var compiled *strategy.CompiledLevel
	for _, candidate := range plan.Levels().All() {
		if candidate.Definition().LevelID == result.LevelID {
			compiled = &candidate
			break
		}
	}
	// A level about an absent series detected nothing, and has no facts.
	if compiled == nil || result.DetectEvidence == nil {
		return level
	}
	evidence := *result.DetectEvidence
	// The algorithm whose facts are given: the one that decided, or for a
	// level no algorithm matched - a recovery - its first.
	decided := 0
	if evidence.MatchedAlgorithmOrdinal != nil {
		decided = int(*evidence.MatchedAlgorithmOrdinal)
	}
	// A level's detectors are its algorithms, one each, when every one of
	// them has a detector; only then does the ordinal name a detector.
	detectors := compiled.Detectors()
	if len(detectors) != compiled.Algorithms().Len() {
		detectors = nil
	}
	for index, algorithm := range compiled.Algorithms().All() {
		entry := contract.DetectionAlgorithmV1{Type: algorithm.Kind()}
		if index == decided {
			var detector *strategy.DetectorSpec
			if index < len(detectors) {
				detector = &detectors[index]
				if normalizer, ok := plan.Normalizer(detector.NormalizerRef()); ok && normalizer.TargetUnit() != "" {
					level.Unit = normalizer.TargetUnit()
				}
			}
			entry.Facts = algorithmFacts(detector, evidence)
		}
		level.Algorithms = append(level.Algorithms, entry)
	}
	return level
}

// algorithmFacts is what the deciding algorithm decided on: the normalized
// value it was judged at; and for a Threshold that matched, its OR group by
// ordinal and each condition of it, its operator as the platform words it
// and its threshold in the unit the value is in. The value comes first, so
// a group of more conditions than an algorithm's facts hold keeps it.
func algorithmFacts(detector *strategy.DetectorSpec, evidence contract.DetectEvidenceV1) contract.DetectionFactsV1 {
	var facts contract.DetectionFactsV1
	if len(evidence.NormalizedValue) > 0 && string(evidence.NormalizedValue) != "null" {
		facts = append(facts, contract.DetectionFactV1{Name: "value", Value: append(json.RawMessage(nil), evidence.NormalizedValue...)})
	}
	if detector != nil && detector.Kind() == strategy.DetectorKindThreshold && evidence.MatchedGroupOrdinal != nil {
		groups := detector.Predicate().Facts().Children
		if ordinal := int(*evidence.MatchedGroupOrdinal); ordinal < len(groups) {
			facts = append(facts, contract.DetectionFactV1{Name: "group", Value: json.RawMessage(strconv.Itoa(ordinal))})
			for position, condition := range groups[ordinal].Children {
				suffix := ""
				if position > 0 {
					suffix = "_" + strconv.Itoa(position+1)
				}
				method, _ := json.Marshal(strings.ToLower(condition.Operator))
				facts = append(facts,
					contract.DetectionFactV1{Name: "method" + suffix, Value: method},
					contract.DetectionFactV1{Name: "threshold" + suffix, Value: decimalFact(condition.NormalizedThreshold)})
			}
		}
	}
	return facts
}

// decimalFact is a canonical decimal as a JSON number, or as a string when
// it is not one.
func decimalFact(decimal string) json.RawMessage {
	var number json.Number
	if err := json.Unmarshal([]byte(decimal), &number); err == nil {
		return json.RawMessage(decimal)
	}
	encoded, _ := json.Marshal(decimal)
	return encoded
}
