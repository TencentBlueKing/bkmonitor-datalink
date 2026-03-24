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
	testCases := map[string]struct {
		tableID TableID
		route   *Route
		err     error
	}{
		"empty table id": {
			"", &Route{
				dataSource: BkMonitor,
			}, ErrEmptyTableID,
		},
		"valid table id": {
			"system.cpu_summary",
			&Route{
				dataSource:  BkMonitor,
				db:          "system",
				measurement: "cpu_summary",
			},
			nil,
		},
		"two stage": {
			"cpu_summary", &Route{
				dataSource: BkMonitor,
				db:         "cpu_summary",
			}, nil,
		},
		"wrong table id": {
			"system.cpu_detail.usage", &Route{
				dataSource:  BkMonitor,
				db:          "system",
				measurement: "cpu_detail",
			}, nil,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			route, err := MakeRouteFromTableID(testCase.tableID)
			if err != nil {
				assert.Equal(t, testCase.err, err)
			} else {
				assert.Equal(t, testCase.route, route)
			}
		})
	}
}

// TestMakeRouteFromMetricName
func TestMakeRouteFromMetricName(t *testing.T) {
	testCases := map[string]struct {
		metricName string
		route      *Route
		err        error
	}{
		"empty": {
			"", &Route{}, ErrMetricMissing,
		},
		"single metric": {
			"usage", &Route{
				dataSource: BkMonitor,
				metricName: "usage",
			}, nil,
		},
		"two stage": {
			"cpu_summary:usage", &Route{
				dataSource: BkMonitor,
				db:         "cpu_summary",
				metricName: "usage",
			}, nil,
		},
		"two stage with all": {
			"cpu_summary:__default__", &Route{
				dataSource: BkMonitor,
				db:         "cpu_summary",
				metricName: "__default__",
			}, nil,
		},
		"two stage with data source": {
			"bkmonitor:cpu_summary:usage", &Route{
				dataSource: BkMonitor,
				db:         "cpu_summary",
				metricName: "usage",
			}, nil,
		},
		"table id + metric": {
			"system:cpu_summary:usage", &Route{
				dataSource:  BkMonitor,
				db:          "system",
				measurement: "cpu_summary",
				metricName:  "usage",
			}, nil,
		},
		"bkmonitor + table id + metric": {
			"bkmonitor:system:cpu_summary:usage", &Route{
				dataSource:  BkMonitor,
				db:          "system",
				measurement: "cpu_summary",
				metricName:  "usage",
			}, nil,
		},
		"bkbase + table id + metric": {
			"bkbase:system:cpu_summary:usage", &Route{
				dataSource:  "bkbase",
				db:          "system",
				measurement: "cpu_summary",
				metricName:  "usage",
			}, nil,
		},
		"bkbase +  metric": {
			"bkbase:::usage", &Route{
				dataSource: "bkbase",
				metricName: "usage",
			}, nil,
		},
		"custom +  metric": {
			"custom:tars_devcloud_1:tars_requests_total", &Route{
				dataSource: "custom",
				db:         "tars_devcloud_1",
				metricName: "tars_requests_total",
			}, nil,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			route, err := MakeRouteFromMetricName(testCase.metricName)
			if err != nil {
				assert.Equal(t, testCase.err, err)
			} else {
				assert.Equal(t, testCase.route, route)
			}
		})
	}
}

// TestMakeRouteFromLabelMatch
func TestMakeRouteFromLabelMatch(t *testing.T) {
	miss := []*labels.Matcher{}
	miss = append(miss, labels.MustNewMatcher(labels.MatchEqual, "__name__", "usage"))

	valid := []*labels.Matcher{}
	valid = append(valid, labels.MustNewMatcher(labels.MatchEqual, "__name__", "usage"))
	valid = append(valid, labels.MustNewMatcher(labels.MatchEqual, "bk_database", "system"))
	valid = append(valid, labels.MustNewMatcher(labels.MatchEqual, "bk_measurement", "cpu_summary"))

	testCases := map[string]struct {
		input []*labels.Matcher
		want  string
		ok    bool
	}{
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
