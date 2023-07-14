// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	ffclient "github.com/thomaspoignant/go-feature-flag"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

// TestPromqlQuery_Test
func TestPromqlQuery_Test(t *testing.T) {
	err := ffclient.Init(ffclient.Config{
		PollingInterval: 1 * time.Minute,
		Context:         context.Background(),
		Retriever:       &featureFlag.CustomRetriever{},
		FileFormat:      "json",
		DataExporter: ffclient.DataExporter{
			FlushInterval:    5 * time.Second,
			MaxEventInMemory: 100,
			Exporter:         &featureFlag.CustomExport{},
		},
	})
	if err != nil {
		panic(err)
	}
	defer ffclient.Close()

	MockSpace(t)
	MockTsDB(t)
	// 基于当前点开始，获取10个点进行累加取平均值
	// 该计算结果应与下面单元测试的一分钟聚合结果吻合

	testCases := map[string]struct {
		spaceUid string
		data     string
		result   string
		err      error
	}{
		"space_vm_sum_count_add_count": {
			spaceUid: "bkcc__2",
			data:     `{"promql":"sum(count_over_time(container_cpu_system_seconds_total[1m])) + count(container_cpu_system_seconds_total)","start":"1669717380","end":"1669717680","step":"1m"}`,
			result:   `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1669717380000,70639],[1669717440000,74007],[1669717500000,79092],[1669717560000,83808],[1669717620000,85899],[1669717680000,85261]]}]}`,
		},
		"space_vm_count": {
			spaceUid: "bkcc__2",
			data:     `{"promql":"count(container_cpu_system_seconds_total)","start":"1669717380","end":"1669717680","step":"1m"}`,
			result:   `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1669717380000,35895],[1669717440000,35900],[1669717500000,39424],[1669717560000,41380],[1669717620000,43604],[1669717680000,42659]]}]}`,
		},
		"promql": {
			spaceUid: "bkcc__2",
			data:     `{"promql":"jvm_memory_bytes_used{} * 100 / on (pod, area) jvm_memory_bytes_max{}","match":"","start":"1689045775","end":"1689046675","step":"60s"}`,
			result:   ``,
		},
	}
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	// mock掉底层请求接口
	ctrl, stubs := FakePromData(t, true)
	defer stubs.Reset()
	defer ctrl.Finish()

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			resp, err1 := handlePromqlQuery(context.Background(), testCase.data, nil, testCase.spaceUid)
			if testCase.err != nil {
				assert.Equal(t, testCase.err, err1)
			} else {
				assert.Nil(t, err1)
				if err1 == nil {
					result, err2 := json.Marshal(resp)
					assert.Nil(t, err2)
					a := string(result)
					assert.Equal(t, testCase.result, a)
				}
			}
		})
	}
}
