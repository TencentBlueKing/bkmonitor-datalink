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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func snapshotFacts(count int, width int) DetectionFactsV1 {
	facts := make(DetectionFactsV1, 0, count)
	for index := 0; index < count; index++ {
		facts = append(facts, DetectionFactV1{Name: fmt.Sprintf("fact_%d", index),
			Value: json.RawMessage(`"` + strings.Repeat("9", width) + `"`)})
	}
	return facts
}

// largestSnapshot is the largest a realistic named strategy decides: four
// outputs under long reference names, three levels of three algorithms each,
// every algorithm with its eight facts at width characters a value.
func largestSnapshot(width int) DetectionSnapshotV1 {
	snapshot := DetectionSnapshotV1{Version: DetectionSnapshotVersion, RefValues: map[string]DetectionRefValueV1{}}
	for index := 0; index < MaxDetectionSnapshotRefValues; index++ {
		snapshot.RefValues[fmt.Sprintf("REQUEST_SUCCESS_RATE_OUTPUT_%02d", index)] = DetectionRefValueV1{
			Value: json.RawMessage("123456.789012"), State: "SUCCESS"}
	}
	for level := uint32(1); level <= MaxDetectionSnapshotLevels; level++ {
		algorithms := make([]DetectionAlgorithmV1, 0, 3)
		for _, kind := range []string{"Threshold", "SimpleRingRatio", "AdvancedYearRound"} {
			algorithms = append(algorithms, DetectionAlgorithmV1{Type: kind, Facts: snapshotFacts(MaxDetectionSnapshotFacts, width)})
		}
		snapshot.Levels = append(snapshot.Levels, DetectionLevelV1{Level: level, Algorithms: algorithms,
			Window: DetectionWindowV1{Required: 3, Observed: 3, Size: 5}, AnomalyBeginTime: 1_791_636_240, Unit: "percent"})
	}
	return snapshot
}

// The largest snapshot a realistic named strategy decides, its facts at
// realistic width, fits whole under the bound: nothing is truncated.
func TestTheLargestRealisticSnapshotFitsWhole(t *testing.T) {
	encoded, truncated, err := EncodeDetectionSnapshotV1(largestSnapshot(8))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("largest realistic snapshot: %d bytes", len(encoded))
	if truncated || len(encoded) > MaxDetectionSnapshotBytes {
		t.Fatalf("%d bytes, truncated %v", len(encoded), truncated)
	}
}

// Past the size, algorithm facts are dropped from the least severe level
// up until it fits, and the snapshot says it was truncated; ref_values and
// windows are never dropped.
func TestASnapshotPastItsSizeDropsFactsFromTheLeastSevereLevelUp(t *testing.T) {
	snapshot := largestSnapshot(48)
	encoded, truncated, err := EncodeDetectionSnapshotV1(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(encoded) > MaxDetectionSnapshotBytes {
		t.Fatalf("%d bytes, truncated %v", len(encoded), truncated)
	}
	var decoded struct {
		RefValues map[string]DetectionRefValueV1 `json:"ref_values"`
		Levels    []struct {
			Level      uint32 `json:"level"`
			Algorithms []struct {
				Facts map[string]json.RawMessage `json:"facts"`
			} `json:"algorithms"`
			Window DetectionWindowV1 `json:"window"`
		} `json:"levels"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.RefValues) != MaxDetectionSnapshotRefValues || len(decoded.Levels) != MaxDetectionSnapshotLevels || !decoded.Truncated {
		t.Fatalf("ref_values %d levels %d truncated %v", len(decoded.RefValues), len(decoded.Levels), decoded.Truncated)
	}
	kept := map[uint32]bool{}
	for _, level := range decoded.Levels {
		if level.Window != (DetectionWindowV1{Required: 3, Observed: 3, Size: 5}) {
			t.Fatalf("level %d lost its window: %+v", level.Level, level.Window)
		}
		kept[level.Level] = len(level.Algorithms[0].Facts) > 0
	}
	// Level 3 is the least severe and gives its facts first; level 1, the
	// most severe, keeps them.
	if kept[3] || !kept[1] {
		t.Fatalf("facts kept by level %v, want level 3's dropped before level 1's", kept)
	}
	if len(snapshot.Levels[2].Algorithms[0].Facts) != MaxDetectionSnapshotFacts {
		t.Fatal("encoding changed the snapshot it was given")
	}
}

// An algorithm with more facts than an algorithm may carry keeps the first
// ones, in order, and the snapshot says it was truncated.
func TestAnAlgorithmPastItsFactsKeepsTheFirstInOrder(t *testing.T) {
	snapshot := DetectionSnapshotV1{Version: DetectionSnapshotVersion, Levels: []DetectionLevelV1{{Level: 1,
		Algorithms: []DetectionAlgorithmV1{{Type: "Threshold", Facts: snapshotFacts(MaxDetectionSnapshotFacts+2, 1)}}}}}
	encoded, truncated, err := EncodeDetectionSnapshotV1(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || !strings.Contains(string(encoded), `"fact_0":"9","fact_1":"9"`) || strings.Contains(string(encoded), "fact_8") {
		t.Fatalf("truncated %v: %s", truncated, encoded)
	}
	want := `{"version":1,"levels":[{"level":1,"algorithms":[{"type":"Threshold","facts":{` +
		`"fact_0":"9","fact_1":"9","fact_2":"9","fact_3":"9","fact_4":"9","fact_5":"9","fact_6":"9","fact_7":"9"}}],` +
		`"window":{"required":0,"observed":0,"size":0}}],"truncated":true}`
	if string(encoded) != want {
		t.Fatalf("encoded\n%s\nwant\n%s", encoded, want)
	}
}

// More outputs or more levels than a snapshot may hold is a snapshot built
// wrong, refused rather than cut.
func TestASnapshotPastItsOutputsOrLevelsIsRefused(t *testing.T) {
	outputs := largestSnapshot(1)
	outputs.RefValues["ONE_MORE"] = DetectionRefValueV1{State: "MISSING"}
	levels := largestSnapshot(1)
	levels.Levels = append(levels.Levels, DetectionLevelV1{Level: 4})
	for name, snapshot := range map[string]DetectionSnapshotV1{"outputs": outputs, "levels": levels} {
		if _, _, err := EncodeDetectionSnapshotV1(snapshot); err == nil {
			t.Errorf("%s: a snapshot past its bound was encoded", name)
		}
	}
}
