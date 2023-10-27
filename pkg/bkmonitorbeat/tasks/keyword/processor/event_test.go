// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package processor

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/input/file"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
)

func TestNewEventProcessor(t *testing.T) {
	testCase := []keyword.ProcessConfig{
		{
			DataID:         1,
			Encoding:       configs.EncodingGBK,
			HasFilter:      false,
			FilterPatterns: make([]string, 0),
			KeywordConfigs: []configs.KeywordConfig{
				{Name: "access", Pattern: `(?P<module>\w+) (?P<message>.*)`},
				{Name: "OOM", Pattern: `kernel: (?P<processName>\w+) invoked oom-killer`},
			},
		},
		{
			DataID:         2,
			Encoding:       configs.EncodingUTF8,
			HasFilter:      false,
			FilterPatterns: make([]string, 0),
			KeywordConfigs: []configs.KeywordConfig{
				{Name: "access", Pattern: `(?P<module>\w+) (?P<message>.*)`},
				{Name: "OOM", Pattern: `kernel: (?P<processName>\w+) invoked oom-killer`},
			},
		},
	}

	for _, processConfig := range testCase {
		evp, err := NewEventProcessor(processConfig)
		if err != nil {
			continue
		}
		if len(evp.rules) != len(processConfig.KeywordConfigs) {
			t.Errorf("keyword rules init error, len(origin)=%d, len(result)=%d",
				len(processConfig.KeywordConfigs), len(evp.rules))
		}
	}
}

func TestEventProcessor_Filter(t *testing.T) {
	evp, err := NewEventProcessor(keyword.ProcessConfig{
		DataID:    2,
		Encoding:  configs.EncodingUTF8,
		HasFilter: true,
		FilterPatterns: []string{
			".*INFO.*",
			"DEBUG",
		},
		KeywordConfigs: make([]configs.KeywordConfig, 0),
	})
	if err != nil {
		t.Errorf("init config error")
		return
	}

	testCase := []struct {
		event       module.LogEvent
		expectedVal bool
	}{
		{
			event: module.LogEvent{
				Text: `Apr 14 15:40:39 VM_1_11_centos kernel: uwsgi invoked oom-killer: gfp_mask=0xd0, order=0, oom_score_adj=0`,
				File: &file.File{
					State: file.NewState(nil, "/var/log/message", "f"),
					ID:    1,
				},
			},
			expectedVal: false,
		},
		{
			event: module.LogEvent{
				Text: `2020-04-14 14:37:37 INFO   19781   access.data  processor.py[214] strategy(483),item(484),total_records(31)`,
				File: &file.File{
					State: file.NewState(nil, "/data/bkee/logs/bkmonitorv3/kernel.log", "f"),
					ID:    2,
				},
			},
			expectedVal: true,
		},
		{
			event: module.LogEvent{
				Text: `2020-04-14 14:37:37 DEBUG   19781   access.data  processor.py[214] test log`,
				File: &file.File{
					State: file.NewState(nil, "/data/bkee/logs/bkmonitorv3/kernel.log", "f"),
					ID:    2,
				},
			},
			expectedVal: true,
		},
	}

	for i, tCase := range testCase {
		isFiltered := evp.Filter(&tCase.event)
		if isFiltered != tCase.expectedVal {
			t.Errorf(`TestCase:%d handle error, expected val(%v), result val(%v)`,
				i, tCase.expectedVal, isFiltered)
		}
	}
}

func TestEventProcessor_Handle(t *testing.T) {
	evp, err := NewEventProcessor(keyword.ProcessConfig{
		DataID:         2,
		Encoding:       configs.EncodingUTF8,
		HasFilter:      false,
		FilterPatterns: make([]string, 0),
		KeywordConfigs: []configs.KeywordConfig{
			{Name: "Access", Pattern: `access\.(?P<module>\w+) +(?P<filename>[^\[]+)`},
			{Name: "OOM", Pattern: `kernel: (?P<processName>\w+) invoked oom-killer`},
		},
	})
	if err != nil {
		t.Errorf("init config error")
		return
	}

	testCase := []struct {
		event             module.LogEvent
		expectedDimension map[string]map[string]string
	}{
		{
			event: module.LogEvent{
				Text: `Apr 14 15:40:39 VM_1_11_centos kernel: uwsgi invoked oom-killer: gfp_mask=0xd0, order=0, oom_score_adj=0`,
				File: &file.File{
					State: file.NewState(nil, "/var/log/message", "f"),
					ID:    1,
				},
			},
			expectedDimension: map[string]map[string]string{
				"OOM": {"processName": "uwsgi"},
			},
		},
		{
			event: module.LogEvent{
				Text: `2020-04-14 14:37:37 INFO   19781   access.data  processor.py[214] strategy(483),item(484),total_records(31)`,
				File: &file.File{
					State: file.NewState(nil, "/data/bkee/logs/bkmonitorv3/kernel.log", "f"),
					ID:    2,
				},
			},
			expectedDimension: map[string]map[string]string{
				"Access": {
					"module":   "data",
					"filename": "processor.py",
				},
			},
		},
	}

	for i, tCase := range testCase {
		results, err := evp.Handle(&tCase.event)
		if err != nil {
			t.Errorf("TestCase:%d handle error, eventData:%v", i, tCase.event)
		}

		res, ok := results.([]keyword.KeywordTaskResult)
		if !ok {
			t.Errorf("TestCase:%d handle error, result format not correct", i)
		}

		for _, result := range res {
			ruleName := result.RuleName
			expectedDimension, exists := tCase.expectedDimension[ruleName]
			if !exists {
				t.Errorf(`TestCase:%d handle error, expected:"Not Matched", result:%v`, i, result)
			}
			if len(expectedDimension) != len(result.Dimensions) {
				t.Errorf(`TestCase:%d handle error, expected len(dimension): %d, result len(dimension):%d`,
					i, len(expectedDimension), len(result.Dimensions))
			}
			for k, v := range expectedDimension {
				resultValue, exists := result.Dimensions[k]
				if !exists || resultValue != v {
					t.Errorf(`TestCase:%d handle error, expected dimension(%s=>%s), result dimension(%s=>%s)`,
						i, k, v, k, resultValue)
				}
			}
		}
	}

}

func BenchmarkEventProcessor_Handle(b *testing.B) {
	evp, err := NewEventProcessor(keyword.ProcessConfig{
		DataID:         2,
		Encoding:       configs.EncodingUTF8,
		HasFilter:      false,
		FilterPatterns: make([]string, 0),
		KeywordConfigs: []configs.KeywordConfig{
			{Name: "OOM", Pattern: ` (?P<hostname>[A-Z0-9a-z_]+) kernel: (?P<processName>\w+) invoked oom-killer`},
		},
	})
	if err != nil {
		b.Errorf("init config error")
		return
	}

	for i := 0; i < b.N; i++ {
		_, err := evp.Handle(&module.LogEvent{
			Text: `Apr 14 15:40:39 VM_1_11_centos kernel: uwsgi invoked oom-killer: gfp_mask=0xd0, order=0, oom_score_adj=0`,
			File: &file.File{
				State: file.NewState(nil, "/var/log/message", "f"),
				ID:    1,
			},
		})
		if err != nil {
			b.Errorf("Benchmark run error")
		}
	}
}
