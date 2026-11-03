// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// detectIntervalGoldenCase is one item compiled with one detect_interval.
type detectIntervalGoldenCase struct {
	name     string
	document func(t *testing.T) json.RawMessage
	// value is the detect_interval the item carries, nil for none.
	value any
}

func detectIntervalGoldenCases() []detectIntervalGoldenCase {
	threshold := func(t *testing.T) json.RawMessage {
		return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"bk_host_id"},
			[]any{[]any{map[string]any{"method": "gte", "threshold": 80}}})
	}
	ringRatio := func(t *testing.T) json.RawMessage {
		return withItemField(t, g4LegacyStrategyDocument(t, 300, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu",
			[]string{"host"}, map[string]any{"floor": 50, "ceil": nil}), "time_delay", 30)
	}
	ringAmplitude := func(t *testing.T) json.RawMessage {
		return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindRingRatioAmplitude, "usage", "system.cpu", []string{"host"},
			map[string]any{"ratio": 1, "shock": 2, "threshold": 3})
	}
	yearAmplitude := func(t *testing.T) json.RawMessage {
		return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindYearRoundAmplitude, "usage", "system.cpu", []string{"host"},
			map[string]any{"days": 2, "method": "gte", "ratio": 2, "shock": 0})
	}
	osRestart := func(t *testing.T) json.RawMessage {
		return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_ip"},
			map[string]any{})
	}
	var cases []detectIntervalGoldenCase
	for _, family := range []struct {
		name     string
		document func(t *testing.T) json.RawMessage
	}{
		{"threshold", threshold}, {"simple ring ratio", ringRatio}, {"ring ratio amplitude", ringAmplitude},
		{"year round amplitude", yearAmplitude}, {"os restart", osRestart},
	} {
		for _, value := range []struct {
			name  string
			value any
		}{{"none", nil}, {"equal", 60}, {"above", 120}} {
			cases = append(cases, detectIntervalGoldenCase{name: family.name + "/" + value.name, document: family.document, value: value.value})
		}
	}
	return cases
}

// detectIntervalGoldenDigests are the sha256 of executionBytes for each case,
// taken on the build before detect_interval became the request step
// (datalink 57e61ae6a). They are fixed bytes, not derived here: an item
// without the field, with one equal to the aggregation interval, or with one
// above it executes exactly as it did.
var detectIntervalGoldenDigests = map[string]string{
	"threshold/none":             "23a48cca604a527be3143c83d8b37929db75e5471f014f054122d47410036c0f",
	"threshold/equal":            "23a48cca604a527be3143c83d8b37929db75e5471f014f054122d47410036c0f",
	"threshold/above":            "29e84b121cd861338ccf72b9d73f48befed0bf18edc0d7a5183f75fbec4fe87d",
	"simple ring ratio/none":     "0ac61e2ee72ad46b974d96b5c9ca29b06513d693f2519eeb8aa82629f99e1568",
	"simple ring ratio/equal":    "0ac61e2ee72ad46b974d96b5c9ca29b06513d693f2519eeb8aa82629f99e1568",
	"simple ring ratio/above":    "eca8a88490b1669bab7bd3802c4b92a90d4304d97548b67742ef69bb22c94f73",
	"ring ratio amplitude/none":  "6bf5195fa6852f41e72954664076ede2cda36eddd49e0e4e03256eee13121688",
	"ring ratio amplitude/equal": "6bf5195fa6852f41e72954664076ede2cda36eddd49e0e4e03256eee13121688",
	"ring ratio amplitude/above": "04fffaed80d617a083f42db99e6e1923d8d82855fbf455060eaa3e46a9685682",
	"year round amplitude/none":  "4822170e4cb94a5822b0b86dc5500bcafcbc3ec6964106ebf20204b2a190ce9a",
	"year round amplitude/equal": "4822170e4cb94a5822b0b86dc5500bcafcbc3ec6964106ebf20204b2a190ce9a",
	"year round amplitude/above": "965319c529cffbd10feac82c5d5ffbad9652bae3c530d86425a5a9ec8601f249",
	"os restart/none":            "f324a01559f9eca3d103e9cc6660c00421805f718277124c772a6a07d55af76a",
	"os restart/equal":           "f324a01559f9eca3d103e9cc6660c00421805f718277124c772a6a07d55af76a",
	"os restart/above":           "72298bfebb8001f90ef0e673867be9e86ca2da8da73a8f47226062799a3b48f9",
}

func TestAnItemNotDetectedFasterThanItAggregatesExecutesAsBefore(t *testing.T) {
	export := os.Getenv("ALARMD_DETECT_INTERVAL_GOLDEN_EXPORT") == "1"
	for _, test := range detectIntervalGoldenCases() {
		t.Run(test.name, func(t *testing.T) {
			document := test.document(t)
			if test.value != nil {
				document = withItemField(t, document, "detect_interval", test.value)
			}
			sum := sha256.Sum256(executionBytes(t, detectIntervalCatalog(t, document)))
			got := hex.EncodeToString(sum[:])
			if export {
				t.Logf("GOLDEN %q: %q,", test.name, got)
				return
			}
			want, ok := detectIntervalGoldenDigests[test.name]
			if !ok {
				t.Fatalf("no golden digest for %s", test.name)
			}
			if got != want {
				t.Fatalf("%s executes differently from before: digest %s, want %s", test.name, got, want)
			}
		})
	}
}
