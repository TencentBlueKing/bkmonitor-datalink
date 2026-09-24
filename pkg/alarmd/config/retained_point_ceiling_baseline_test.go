// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

type ceilingBaseline struct {
	Inputs struct {
		MaxEncodedBytes          int    `json:"max_encoded_bytes"`
		MaxRequiredHistoryPoints uint32 `json:"max_required_history_points"`
		MaxLevelsPerPlan         int    `json:"max_levels_per_plan"`
	} `json:"inputs"`
	Packed struct {
		Ceilings []uint32 `json:"ceilings"`
	} `json:"packed"`
	Envelope struct {
		Ceilings []uint32 `json:"ceilings"`
	} `json:"envelope"`
}

func readCeilingBaseline(t *testing.T) ceilingBaseline {
	t.Helper()
	raw, err := os.ReadFile("testdata/retained-point-ceilings.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline ceilingBaseline
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	return baseline
}

// The ceiling table is the one that was written down, entry for entry.
//
// The table is derived, so nothing in a review shows it moving: a change to the
// stored representation, to the encoder's per-point cost, or to
// max_encoded_bytes rewrites every entry silently, and what that produces is a
// per-series write refusal with nothing to say why. Pinned to a committed file
// so the move arrives as a diff somebody has to agree with rather than as a
// number nobody was looking at.
//
// A change here is not necessarily wrong. It is necessarily something to state:
// update the file, and say in the same change which of the three inputs moved
// and why the new numbers are the right ones.
func TestTheRetainedPointCeilingsAreTheOnesRecorded(t *testing.T) {
	baseline := readCeilingBaseline(t)
	cfg := completePhaseTwoProductionConfig(validGoAccessConfigObject().withDerivedCapacity(containerShapes()[0]))
	limits := cfg.CompilerLimits()

	// The inputs first. Without them a table that matches proves nothing - the
	// numbers would have been derived from different inputs than the ones the
	// file says produced them, and the agreement would be a coincidence.
	if cfg.Limits.Codec.MaxEncodedBytes != baseline.Inputs.MaxEncodedBytes ||
		limits.MaxRequiredHistoryPoints != baseline.Inputs.MaxRequiredHistoryPoints ||
		limits.MaxLevelsPerPlan != baseline.Inputs.MaxLevelsPerPlan {
		t.Fatalf("inputs are max_encoded_bytes=%d points=%d levels=%d, recorded as %d/%d/%d: the recorded "+
			"table describes a configuration this build no longer has",
			cfg.Limits.Codec.MaxEncodedBytes, limits.MaxRequiredHistoryPoints, limits.MaxLevelsPerPlan,
			baseline.Inputs.MaxEncodedBytes, baseline.Inputs.MaxRequiredHistoryPoints, baseline.Inputs.MaxLevelsPerPlan)
	}
	if !reflect.DeepEqual(limits.MaxRetainedPointsByLevels, baseline.Packed.Ceilings) {
		t.Fatalf("ceilings = %v, recorded %v; say which input moved and why the new table is right",
			limits.MaxRetainedPointsByLevels, baseline.Packed.Ceilings)
	}
}

// The recorded envelope table is what the envelope encoder still produces, so
// the comparison the file draws is against a real number rather than a
// remembered one.
//
// Without this the envelope column is a story: it would go on saying 2087 after
// the envelope encoder changed, and the ratio decision-021 is argued from would
// quietly stop being true while still being printed.
func TestTheRecordedEnvelopeTableIsWhatTheEnvelopeStillCosts(t *testing.T) {
	baseline := readCeilingBaseline(t)
	for count := 1; count < len(baseline.Envelope.Ceilings); count++ {
		want := baseline.Envelope.Ceilings[count]
		if want == 0 {
			continue
		}
		points, err := state.MaxRuntimeEnvelopePoints(count, baseline.Inputs.MaxEncodedBytes)
		if err != nil {
			t.Fatalf("envelope bound for %d Levels: %v", count, err)
		}
		if uint32(points) != want {
			t.Fatalf("envelope holds %d points at %d Levels, recorded %d; the comparison the file draws "+
				"is no longer against what the envelope costs", points, count, want)
		}
	}
	// The point of recording both: the framed record has to be the larger one,
	// at the most crowded Level count as well as at one Level. A change that
	// moved them together would leave every other assertion here passing.
	packed, envelope := baseline.Packed.Ceilings, baseline.Envelope.Ceilings
	for count := 1; count < len(packed) && count < len(envelope); count++ {
		if envelope[count] == 0 {
			continue
		}
		if packed[count] <= envelope[count] {
			t.Fatalf("at %d Levels the framed record holds %d points against the envelope's %d; the change "+
				"was made because it holds more", count, packed[count], envelope[count])
		}
	}
}
