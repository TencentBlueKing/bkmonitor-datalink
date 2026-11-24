// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A unit note is named only on the kinds that read a threshold prefix. The
// ratio and year-on-year kinds compile with the unit as well -- so an
// unreadable prefix is still read for them, or their compile would refuse --
// but they convert both sides alike, and a note there would name a prefix that
// changes nothing.
func TestUnitNotesAreNamedOnlyWhereThePrefixIsRead(t *testing.T) {
	for kind, reads := range map[string]bool{
		strategy.DetectorKindThreshold:          true,
		strategy.DetectorKindYearRoundAmplitude: true,
		strategy.DetectorKindRingRatioAmplitude: true,
		strategy.DetectorKindYearRoundRange:     true,
		strategy.DetectorKindAdvancedRingRatio:  false,
		strategy.DetectorKindSimpleYearRound:    false,
	} {
		if got := algorithmReadsPrefix(kind); got != reads {
			t.Errorf("algorithmReadsPrefix(%s) = %t, want %t", kind, got, reads)
		}
		if !algorithmReadsUnit(kind) {
			t.Errorf("%s compiles with the unit and must still have an unreadable prefix read for it", kind)
		}
	}
	// The reading itself does not depend on the kind: an outside prefix is
	// read as not given, the unit kept, for every kind that compiles with it.
	reading := readAlgorithmUnit(expressionDataUnit{unit: "ms", basis: DataUnitBasisQueryUnit}, "Gi")
	if reading.dataUnit != "ms" || reading.prefix != "" || reading.note != ReasonUnitPrefixOutsideStepsReadAsSmallest {
		t.Fatalf("an outside prefix reads as %+v, want unit ms, no prefix, noted", reading)
	}
}
