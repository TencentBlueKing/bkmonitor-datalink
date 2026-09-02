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
	"bytes"
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const normalizedDecimalScale int64 = 1_000_000

var decimalPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

func (s NumericNormalizerSpec) Normalize(raw json.RawMessage) NormalizeResult {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return NormalizeResult{reasonCode: contract.ReasonRequiredValueMissing}
	}
	if trimmed[0] == '"' || trimmed[0] == '{' || trimmed[0] == '[' || bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) {
		return NormalizeResult{reasonCode: contract.ReasonRequiredValueTypeMismatch}
	}
	number := string(trimmed)
	matches := decimalPattern.FindStringSubmatch(number)
	if matches == nil {
		return NormalizeResult{reasonCode: contract.ReasonRequiredValueTypeMismatch}
	}
	rational, ok := parseDecimalRationalMatch(number, matches, true)
	if !ok {
		return NormalizeResult{reasonCode: contract.ReasonRequiredValueNormalizationFailed}
	}
	value, ok := normalizeRational(rational, s.sourceMultiplier)
	if !ok {
		return NormalizeResult{reasonCode: contract.ReasonRequiredValueNormalizationFailed}
	}
	return NormalizeResult{value: value}
}

func normalizeRational(value *big.Rat, multiplier int64) (NormalizedNumber, bool) {
	if value == nil || multiplier <= 0 {
		return NormalizedNumber{}, false
	}
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt64(multiplier))
	scaled.Mul(scaled, new(big.Rat).SetInt64(normalizedDecimalScale))
	numerator := scaled.Num()
	denominator := scaled.Denom()
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	absRemainder := new(big.Int).Abs(new(big.Int).Set(remainder))
	twiceRemainder := new(big.Int).Lsh(absRemainder, 1)
	comparison := twiceRemainder.Cmp(denominator)
	if comparison > 0 || (comparison == 0 && quotient.Bit(0) == 1) {
		if numerator.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return normalizedNumberFromBigInt(quotient)
}

func parseDecimalRational(value string, allowExponent bool) (*big.Rat, bool) {
	if len(value) == 0 || len(value) > 128 {
		return nil, false
	}
	matches := decimalPattern.FindStringSubmatch(value)
	if matches == nil {
		return nil, false
	}
	return parseDecimalRationalMatch(value, matches, allowExponent)
}

func parseDecimalRationalMatch(value string, matches []string, allowExponent bool) (*big.Rat, bool) {
	if len(value) == 0 || len(value) > 128 || len(matches) != 5 || (!allowExponent && matches[4] != "") {
		return nil, false
	}
	exponent := 0
	if matches[4] != "" {
		parsed, err := strconv.Atoi(matches[4])
		if err != nil || parsed < -128 || parsed > 128 {
			return nil, false
		}
		exponent = parsed
	}
	digits := matches[2] + matches[3]
	numerator := new(big.Int)
	if _, ok := numerator.SetString(digits, 10); !ok {
		return nil, false
	}
	if matches[1] == "-" {
		numerator.Neg(numerator)
	}
	scale := len(matches[3]) - exponent
	denominator := big.NewInt(1)
	if scale > 0 {
		denominator.Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	} else if scale < 0 {
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil)
		numerator.Mul(numerator, multiplier)
	}
	return new(big.Rat).SetFrac(numerator, denominator), true
}

type unitSpec struct {
	unitID        string
	targetUnit    string
	suffixes      []string
	defaultIndex  int
	factorsToBase []int64
}

func compileUnitNormalizer(dataUnit, thresholdPrefix string) (NumericNormalizerSpec, int64, bool) {
	unitID := dataUnit
	if parts := strings.Split(dataUnit, "||"); len(parts) == 2 {
		unitID = parts[1]
	}
	spec, ok := unitSpecFor(unitID)
	if !ok {
		return NumericNormalizerSpec{}, 0, false
	}
	thresholdMultiplier := int64(1)
	if thresholdPrefix != "" {
		index := -1
		for candidate, suffix := range spec.suffixes {
			if suffix == thresholdPrefix {
				index = candidate
				break
			}
		}
		if index < 0 {
			return NumericNormalizerSpec{}, 0, false
		}
		thresholdMultiplier = spec.factorsToBase[index]
	}
	normalizer := NumericNormalizerSpec{
		sourceUnit: dataUnit, targetUnit: spec.targetUnit, sourceMultiplier: spec.factorsToBase[spec.defaultIndex],
		decimalPlaces: 6, rounding: "HALF_EVEN",
	}
	digest, err := contract.DeriveCanonicalDigestV2("numeric-normalizer-v1", normalizerWire{
		SourceUnit: normalizer.sourceUnit, TargetUnit: normalizer.targetUnit, SourceMultiplier: normalizer.sourceMultiplier,
		DecimalPlaces: normalizer.decimalPlaces, Rounding: normalizer.rounding,
	})
	if err != nil {
		return NumericNormalizerSpec{}, 0, false
	}
	normalizer.ref = digest
	return normalizer, thresholdMultiplier, true
}

func unitSpecFor(unitID string) (unitSpec, bool) {
	identity := map[string]struct{}{"": {}, "none": {}, "short": {}, "celsius": {}, "fahrenheit": {}, "kelvin": {}}
	if _, ok := identity[unitID]; ok {
		return unitSpec{unitID: unitID, targetUnit: unitID, suffixes: []string{""}, factorsToBase: []int64{1}}, true
	}
	if unitID == "percent" || unitID == "percentunit" {
		defaultIndex := 0
		if unitID == "percentunit" {
			defaultIndex = 1
		}
		return unitSpec{unitID: unitID, targetUnit: "%", suffixes: []string{"%", "x100%"}, defaultIndex: defaultIndex, factorsToBase: []int64{1, 100}}, true
	}
	binaryIDs := map[string]int{"bits": 0, "bytes": 0, "kbytes": 1, "mbytes": 2, "gbytes": 3, "tbytes": 4, "pbytes": 5}
	if index, ok := binaryIDs[unitID]; ok {
		return unitSpec{unitID: unitID, targetUnit: "base", suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei"}, defaultIndex: index, factorsToBase: powers(1024, 7)}, true
	}
	decimalIDs := map[string]int{
		"decbits": 0, "decbytes": 0, "pps": 0, "bps": 0, "Bps": 0, "hertz": 0,
		"deckbytes": 1, "KBs": 1, "Kbits": 1,
		"decmbytes": 2, "MBs": 2, "Mbits": 2,
		"decgbytes": 3, "GBs": 3, "Gbits": 3,
		"dectbytes": 4, "TBs": 4, "Tbits": 4,
		"decpbytes": 5, "PBs": 5, "Pbits": 5,
	}
	if index, ok := decimalIDs[unitID]; ok {
		return unitSpec{unitID: unitID, targetUnit: "base", suffixes: []string{"", "k", "M", "G", "T", "P", "E"}, defaultIndex: index, factorsToBase: powers(1000, 7)}, true
	}
	timeIDs := map[string]int{"ns": 0, "µs": 1, "ms": 2, "s": 3, "m": 4, "h": 5, "d": 6}
	if index, ok := timeIDs[unitID]; ok {
		return unitSpec{
			unitID: unitID, targetUnit: "ns", suffixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, defaultIndex: index,
			factorsToBase: []int64{1, 1_000, 1_000_000, 1_000_000_000, 60_000_000_000, 3_600_000_000_000, 86_400_000_000_000},
		}, true
	}
	countIDs := map[string]struct{}{"cps": {}, "ops": {}, "reqps": {}, "rps": {}, "wps": {}, "iops": {}, "cpm": {}, "opm": {}, "rpm": {}, "wpm": {}}
	if _, ok := countIDs[unitID]; ok {
		return unitSpec{unitID: unitID, targetUnit: "base", suffixes: []string{"", "K", "M", "B", "T"}, factorsToBase: powers(1000, 5)}, true
	}
	return unitSpec{}, false
}

func powers(factor int64, count int) []int64 {
	result := make([]int64, count)
	result[0] = 1
	for index := 1; index < count; index++ {
		result[index] = result[index-1] * factor
	}
	return result
}
