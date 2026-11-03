package state

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What a Runtime State record costs, and what the packed codec in this package
// can and cannot say about one.
//
// Both halves are here because decision-021 turns on the difference. The cost
// is why the representation should change; the packed codec's losses are why
// the representation it changes to cannot simply be the one already sitting in
// codec.go, which was written for a consumer that never needed the facts it
// drops.

type recordBaseline struct {
	Fixture struct {
		Points           int `json:"points"`
		DigestHexChars   int `json:"digest_hex_chars"`
		ValueBudgetBytes int `json:"value_budget_bytes"`
	} `json:"fixture"`
	Levels []struct {
		Count         int     `json:"count"`
		Bytes         int     `json:"bytes"`
		BytesPerPoint float64 `json:"bytes_per_point"`
		CeilingPoints int     `json:"ceiling_points"`
	} `json:"levels"`
	ClusterReading struct {
		ObservedOn       string `json:"observed_on"`
		Series           int    `json:"series"`
		PointsPerSeries  int    `json:"points_per_series"`
		Levels           int    `json:"levels"`
		BytesReadPerSlot int64  `json:"bytes_read_per_slot"`
	} `json:"cluster_reading"`
}

func loadRecordBaseline(t *testing.T) recordBaseline {
	t.Helper()
	raw, err := os.ReadFile("testdata/runtime-record-baseline.json")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	var baseline recordBaseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("decode baseline: %v", err)
	}
	if len(baseline.Levels) == 0 || baseline.Fixture.Points == 0 {
		t.Fatal("baseline carries no rows; a comparison against nothing always passes")
	}
	return baseline
}

// encodedRecordBytes marshals the record the execution store writes, with the
// history the fixture describes. The digests are full width because that is
// what the record holds in production and a narrow fixture would report the
// fixture's slack as the representation's.
func encodedRecordBytes(t *testing.T, points, levels, digestChars int) int {
	t.Helper()
	digest := strings.Repeat("ab", digestChars/2)
	mutations := make([]execution.RuntimeLevelStateMutation, levels)
	for index := range mutations {
		mutations[index] = execution.RuntimeLevelStateMutation{LevelID: uint32(index + 1),
			LevelStateCompatibility: "COMPATIBLE", HistoryCompleteness: execution.HistoryFull}
	}
	history := make([]execution.StateHistoryPoint, points)
	for point := range history {
		facts := make([]execution.StateLevelFact, levels)
		for index := range facts {
			facts[index] = execution.StateLevelFact{LevelID: uint32(index + 1),
				DetectFingerprint: digest, Result: execution.LevelFactNormal}
		}
		history[point] = execution.StateHistoryPoint{RecordID: fmt.Sprintf("%064x", point),
			SourceTime: 1758400000 + int64(point)*60, Levels: facts}
	}
	raw, err := json.Marshal(runtimeEnvelope{Schema: executionStateSchemaV2, BlobRevision: 7,
		Levels: mutations, History: history})
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return len(raw)
}

// TestTheStoredRecordCostsWhatTheBaselineRecords compares the real encoder
// against the recorded numbers.
//
// A failure is not an invitation to regenerate the file. The ceilings the
// compiler admits Plans against are derived from this arithmetic, so a record
// that changed shape moved them, and the whole point of writing them down was
// that the move be visible rather than silent.
func TestTheStoredRecordCostsWhatTheBaselineRecords(t *testing.T) {
	baseline := loadRecordBaseline(t)
	for _, row := range baseline.Levels {
		encoded := encodedRecordBytes(t, baseline.Fixture.Points, row.Count, baseline.Fixture.DigestHexChars)
		if encoded != row.Bytes {
			t.Errorf("a %d-Level record over %d points encodes to %d bytes, baseline says %d; the record "+
				"changed shape, so the point ceilings derived from it moved too - update both together",
				row.Count, baseline.Fixture.Points, encoded, row.Bytes)
		}
		ceiling, err := MaxRuntimeEnvelopePoints(row.Count, baseline.Fixture.ValueBudgetBytes)
		if err != nil {
			t.Fatalf("derive ceiling for %d Levels: %v", row.Count, err)
		}
		if ceiling != row.CeilingPoints {
			t.Errorf("a %d-Level Plan may retain %d points, baseline says %d", row.Count, ceiling, row.CeilingPoints)
		}
	}
}

// TestTheEncoderAgreesWithWhatTheClusterRead checks the arithmetic against the
// deployment rather than against itself.
//
// Every other number here is produced by the same code it is asserted about,
// which can only report self-consistency. This one reading comes from outside:
// if the model and the cluster ever disagree, either the record is not what
// this package thinks it is or the reading is measuring something else, and
// both are worth stopping for.
func TestTheEncoderAgreesWithWhatTheClusterRead(t *testing.T) {
	baseline := loadRecordBaseline(t)
	reading := baseline.ClusterReading
	if reading.Series == 0 || reading.PointsPerSeries == 0 || reading.BytesReadPerSlot == 0 {
		t.Fatal("the recorded reading is incomplete; a check against zero passes whatever the encoder does")
	}
	observedPerPoint := float64(reading.BytesReadPerSlot) / float64(reading.Series*reading.PointsPerSeries)
	encoded := encodedRecordBytes(t, reading.PointsPerSeries, reading.Levels, baseline.Fixture.DigestHexChars)
	modelPerPoint := float64(encoded) / float64(reading.PointsPerSeries)

	const tolerance = 0.02
	apart := math.Abs(observedPerPoint-modelPerPoint) / modelPerPoint
	if apart > tolerance {
		t.Errorf("the deployment read %.1f bytes per point on %s, this package's encoder says %.1f (%.1f%% apart, "+
			"tolerance %.0f%%); the stored record is not what this package models it to be, or the reading counts "+
			"something else", observedPerPoint, reading.ObservedOn, modelPerPoint, 100*apart, 100*tolerance)
	}
}
