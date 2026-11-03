// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
)

// DetectionSnapshotVersion is the version of the detection snapshot an event
// carries. A field is only ever added; a change of meaning is a new version.
const DetectionSnapshotVersion = 1

// The bounds of one event's detection snapshot, part of its contract: at
// most as many ref_values as a query has named outputs, as many levels as a
// strategy has, and as many scalar facts per algorithm as a reader needs to
// word it; and the whole object, encoded, no larger than
// MaxDetectionSnapshotBytes. Past the size, algorithm facts go first, from
// the least severe level up, and the snapshot says it was truncated;
// ref_values and windows are never dropped.
const (
	MaxDetectionSnapshotRefValues = 4
	MaxDetectionSnapshotLevels    = 3
	MaxDetectionSnapshotFacts     = 8
	MaxDetectionSnapshotBytes     = 4 << 10
)

// What became of a detection snapshot an event was to carry: built whole,
// built truncated to its bound, or dropped - past its size with no algorithm
// facts left to drop - and the event sent without it. A snapshot is never
// a reason not to send its event.
const (
	DetectionSnapshotWhole     = "whole"
	DetectionSnapshotTruncated = "truncated"
	DetectionSnapshotDropped   = "dropped"
)

// DetectionSnapshotResults is every result.
var DetectionSnapshotResults = []string{DetectionSnapshotWhole, DetectionSnapshotTruncated, DetectionSnapshotDropped}

// DetectionSnapshotV1 is what a detection decided on, carried on its
// ABNORMAL and RECOVERY events under extra_data.detection_snapshot: the
// value of each named output at the decided record, on ABNORMAL only, and
// each decided level's algorithms, window, anomaly start and unit. Only the
// point that decided, never a series.
type DetectionSnapshotV1 struct {
	Version   int                            `json:"version"`
	RefValues map[string]DetectionRefValueV1 `json:"ref_values,omitempty"`
	Levels    []DetectionLevelV1             `json:"levels"`
	Truncated bool                           `json:"truncated,omitempty"`
}

// DetectionRefValueV1 is one named output at the decided record: its state
// (SUCCESS, PARTIAL, MISSING, ERROR or UNSUPPORTED), and its value when the
// state is SUCCESS or PARTIAL.
type DetectionRefValueV1 struct {
	Value json.RawMessage `json:"value,omitempty"`
	State string          `json:"state"`
}

// DetectionLevelV1 is one decided level.
type DetectionLevelV1 struct {
	Level            uint32                 `json:"level"`
	Algorithms       []DetectionAlgorithmV1 `json:"algorithms"`
	Window           DetectionWindowV1      `json:"window"`
	AnomalyBeginTime int64                  `json:"anomaly_begin_time,omitempty"`
	Unit             string                 `json:"unit,omitempty"`
}

// DetectionWindowV1 is a level's trigger window: the anomalies it needs, the
// anomalies it holds, and its size, in points.
type DetectionWindowV1 struct {
	Required uint32 `json:"required"`
	Observed uint32 `json:"observed"`
	Size     uint32 `json:"size"`
}

// DetectionAlgorithmV1 is one of a level's algorithms: its type and the
// scalar facts it decided on, in the order they are listed.
type DetectionAlgorithmV1 struct {
	Type  string           `json:"type"`
	Facts DetectionFactsV1 `json:"facts,omitempty"`
}

// DetectionFactV1 is one scalar fact: a name and a JSON scalar.
type DetectionFactV1 struct {
	Name  string
	Value json.RawMessage
}

// DetectionFactsV1 encodes as a JSON object in the order its facts are
// listed, so a snapshot encodes the same bytes every time.
type DetectionFactsV1 []DetectionFactV1

// MarshalJSON writes the facts as one object, in order.
func (facts DetectionFactsV1) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, fact := range facts {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, err := json.Marshal(fact.Name)
		if err != nil {
			return nil, err
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		if len(fact.Value) == 0 {
			buffer.WriteString("null")
		} else {
			buffer.Write(fact.Value)
		}
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// UnmarshalJSON reads an object of scalar facts back, in its order.
func (facts *DetectionFactsV1) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return errors.New("alarmd contract: detection facts must be an object")
	}
	read := DetectionFactsV1{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		read = append(read, DetectionFactV1{Name: name, Value: value})
	}
	*facts = read
	return nil
}

// EncodeDetectionSnapshotV1 is the snapshot as an event carries it, held to
// its bounds: ref_values past MaxDetectionSnapshotRefValues and levels past
// MaxDetectionSnapshotLevels are refused, as a snapshot built wrong; facts
// past MaxDetectionSnapshotFacts are cut; and while the encoding is past
// MaxDetectionSnapshotBytes, the algorithm facts of the least severe level
// still holding any - the highest level number - are dropped. Anything cut
// or dropped sets Truncated. The snapshot passed is not changed.
func EncodeDetectionSnapshotV1(snapshot DetectionSnapshotV1) (json.RawMessage, bool, error) {
	if len(snapshot.RefValues) > MaxDetectionSnapshotRefValues || len(snapshot.Levels) > MaxDetectionSnapshotLevels {
		return nil, false, errors.New("alarmd contract: detection snapshot holds more ref_values or levels than it may")
	}
	bounded := snapshot
	bounded.Levels = make([]DetectionLevelV1, len(snapshot.Levels))
	for index, level := range snapshot.Levels {
		level.Algorithms = append([]DetectionAlgorithmV1(nil), level.Algorithms...)
		for position, algorithm := range level.Algorithms {
			if len(algorithm.Facts) > MaxDetectionSnapshotFacts {
				level.Algorithms[position].Facts = algorithm.Facts[:MaxDetectionSnapshotFacts:MaxDetectionSnapshotFacts]
				bounded.Truncated = true
			}
		}
		bounded.Levels[index] = level
	}
	// The least severe first: the highest level number, then the next.
	order := make([]int, len(bounded.Levels))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		return bounded.Levels[order[left]].Level > bounded.Levels[order[right]].Level
	})
	for {
		encoded, err := json.Marshal(bounded)
		if err != nil {
			return nil, false, err
		}
		if len(encoded) <= MaxDetectionSnapshotBytes {
			return encoded, bounded.Truncated, nil
		}
		dropped := false
		for _, index := range order {
			level := &bounded.Levels[index]
			for position := range level.Algorithms {
				if len(level.Algorithms[position].Facts) > 0 {
					level.Algorithms[position].Facts = nil
					dropped = true
				}
			}
			if dropped {
				break
			}
		}
		if !dropped {
			// Nothing left to drop: ref_values and windows stay, and a
			// snapshot of four outputs and three levels fits by far.
			return nil, false, errors.New("alarmd contract: detection snapshot is past its size with no algorithm facts left to drop")
		}
		bounded.Truncated = true
	}
}
