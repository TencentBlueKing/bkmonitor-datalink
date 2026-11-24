// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sourceliveness_test

import (
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// The answers design §8 read from the provider, and the failures around
// them: only data is alive. An empty answer with a status (a table, field or
// space the provider does not know) is unknown, and so in this version is an
// empty one without a status; a partial answer is unknown even beside data.
func TestOnlyDataIsAlive(t *testing.T) {
	for _, test := range []struct {
		name   string
		answer sourceliveness.Answer
		want   sourceliveness.Verdict
	}{
		{"data", sourceliveness.Answer{Points: 1}, sourceliveness.Verdict{Result: sourceliveness.ResultAlive}},
		{"empty with a status", sourceliveness.Answer{Status: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonStatus}},
		{"data with a status", sourceliveness.Answer{Status: "SPACE_IS_NOT_EXISTS", Points: 3},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonStatus}},
		{"empty without a status", sourceliveness.Answer{},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonEmptyWithoutStatus}},
		{"partial with data", sourceliveness.Answer{Partial: true, Points: 2},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonPartial}},
		{"partial by its status", sourceliveness.Answer{Status: "QUERY_TS_PARTIAL", Points: 2},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonPartial}},
		{"timeout", sourceliveness.Answer{Timeout: true, Err: errors.New("deadline")},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonTimeout}},
		{"error", sourceliveness.Answer{Err: errors.New("502")},
			sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonError}},
	} {
		if got := sourceliveness.Classify(test.answer); got != test.want {
			t.Errorf("%s: %+v, want %+v", test.name, got, test.want)
		}
	}
}

// The first version has no silent result: a source that answers empty
// without a status is unknown, and the closed sets say so.
func TestThereIsNoSilentInTheFirstVersion(t *testing.T) {
	for _, result := range sourceliveness.Results {
		if result == "silent" {
			t.Fatal("silent is a result")
		}
	}
	if len(sourceliveness.Results) != 3 {
		t.Fatalf("results %v, want alive, unknown and unsupported", sourceliveness.Results)
	}
}

// The reasons are closed and the same everywhere they are read: five for an
// unknown source, six for an unsupported one.
func TestTheReasonSetsAreClosed(t *testing.T) {
	unknown := []sourceliveness.Reason{"status", "partial", "timeout", "error", "empty_without_status"}
	unsupported := []sourceliveness.Reason{"promql", "bkdata", "apm", "multi_table", "data_label", "event_sparse"}
	for _, test := range []struct {
		got, want []sourceliveness.Reason
	}{{sourceliveness.UnknownReasons, unknown}, {sourceliveness.UnsupportedReasons, unsupported}} {
		if len(test.got) != len(test.want) {
			t.Fatalf("reasons %v, want %v", test.got, test.want)
		}
		for index := range test.want {
			if test.got[index] != test.want[index] {
				t.Fatalf("reasons %v, want %v", test.got, test.want)
			}
		}
	}
}
