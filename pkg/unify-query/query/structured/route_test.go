// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
)

type tableDrivenCase map[string]struct {
	input string
	want  string
	ok    bool
}

// TestMakeRouteFromTableID
func TestMakeRouteFromTableID(t *testing.T) {
	testCases := tableDrivenCase{
		"empty": {"", "", false},
		"miss":  {"usage", "", false},
		"valid": {"system.cpu_summary", "bkmonitor:system:cpu_summary:", true},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			route, err := MakeRouteFromTableID(testCase.input)
			if testCase.ok {
				assert.Nil(t, err)
				assert.Equal(t, testCase.want, route.RealMetricName())
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestMakeRouteFromMetricName
func TestMakeRouteFromMetricName(t *testing.T) {
	testCases := tableDrivenCase{
		"empty":               {"", "", false},
		"only metric name":    {"usage", "bkmonitor:::usage", true},
		"contain metric":      {":usage", "bkmonitor:::usage", true},
		"contain metric2":     {"cpu_summary:usage", "cpu_summary:::usage", true},
		"not contain metric2": {"cpu_summary:", "", false},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			route, err := MakeRouteFromMetricName(testCase.input)
			if testCase.ok {
				assert.Nil(t, err)
				assert.Equal(t, testCase.want, route.RealMetricName())
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestMakeRouteFromLabelMatch
func TestMakeRouteFromLabelMatch(t *testing.T) {
	type tableDrivenCase map[string]struct {
		input []*labels.Matcher
		want  string
		ok    bool
	}

	miss := []*labels.Matcher{}
	miss = append(miss, labels.MustNewMatcher(labels.MatchEqual, "__name__", "usage"))

	valid := []*labels.Matcher{}
	valid = append(valid, labels.MustNewMatcher(labels.MatchEqual, "__name__", "usage"))
	valid = append(valid, labels.MustNewMatcher(labels.MatchEqual, "bk_database", "system"))
	valid = append(valid, labels.MustNewMatcher(labels.MatchEqual, "bk_measurement", "cpu_summary"))

	testCases := tableDrivenCase{
		"empty": {[]*labels.Matcher{}, "", false},
		"miss":  {miss, "", false},
		"valid": {valid, "bkmonitor:system:cpu_summary:usage", true},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			route, err := MakeRouteFromLabelMatch(testCase.input)
			if testCase.ok {
				assert.Nil(t, err)
				assert.Equal(t, testCase.want, route.RealMetricName())
			} else {
				assert.Error(t, err)
			}
		})
	}
}
