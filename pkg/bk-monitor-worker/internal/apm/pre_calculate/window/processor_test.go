// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// traceFileDirectory 测试 Trace 存放目录
const traceFileDirectory = "test_traces/"

func fileExceptToTypeInstance(fileName string, fileContentType string) any {
	content, err := os.ReadFile(fmt.Sprintf("%s%s", traceFileDirectory, fileName))
	if err != nil {
		panic(err)
	}

	if fileContentType == "map" {
		traceInfo := make(map[string]any)
		if err := json.Unmarshal(content, &traceInfo); err != nil {
			panic(err)
		}
		res, err := json.Marshal(traceInfo)
		if err != nil {
			panic(err)
		}

		return res
	} else if fileContentType == "list" {
		var strList []string
		if err := json.Unmarshal(content, &strList); err != nil {
			panic(err)
		}
		return strList
	}
	panic("Not support")
}

// fileTracesToEvent convert test_traces/xx.json to Event
func fileTracesToEvent(fileName string) Event {
	f, err := os.Open(fmt.Sprintf("%s%s", traceFileDirectory, fileName))
	if err != nil {
		panic(err)
	}

	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}

	var spans []Span
	if err := json.Unmarshal(content, &spans); err != nil {
		panic(err)
	}
	if len(spans) == 0 {
		panic("span list is empty!")
	}

	graph := NewDiGraph()
	for _, span := range spans {
		graph.AddNode(Node{StandardSpan: ToStandardSpan(span)})
	}
	graph.RefreshEdges()

	return Event{
		ReleaseCount: int64(graph.Length()),
		CollectTrace: CollectTrace{
			TraceId: spans[0].TraceId,
			Graph:   graph,
			Runtime: NewRuntimeStrategies(
				RuntimeConfig{},
				[]ReentrantRuntimeStrategy{ReentrantLogRecord, ReentrantLimitMaxCount, RefreshUpdateTime},
				[]ReentrantRuntimeStrategy{PredicateLimitMaxDuration, PredicateNoDataDuration},
			).handleNew(),
		},
	}
}

func TestProcessorHandleResult(t *testing.T) {
	dataId := "12345"
	p := initialProcessor(t, dataId, false)

	resultFilter := func(requests []storage.SaveRequest) []storage.SaveRequest { return requests }
	t.Run("single-trace", func(t *testing.T) {
		if !runCase(
			p,
			"single.json",
			[]storage.SaveRequest{
				{
					Target: storage.BloomFilter,
					Data: storage.BloomStorageData{
						DataId: dataId,
						Key:    "5d8f5140b03d2ffaa51fe278ed020d88",
					},
				},
				{
					Target: storage.SaveEs,
					Data: storage.EsStorageData{
						DataId:     dataId,
						DocumentId: "5d8f5140b03d2ffaa51fe278ed020d88",
						Value:      fileExceptToTypeInstance("single-expect.json", "map").([]byte),
					},
				},
			},
			resultFilter) {
			t.Fatal("Not equal")
		}
	})

	t.Run("complex-trace", func(t *testing.T) {
		if !runCase(
			p,
			"complex.json",
			[]storage.SaveRequest{
				{
					Target: storage.BloomFilter,
					Data: storage.BloomStorageData{
						DataId: dataId,
						Key:    "3775128e80c1da365aa8437eb7fc21ad",
					},
				},
				{
					Target: storage.SaveEs,
					Data: storage.EsStorageData{
						DataId:     dataId,
						DocumentId: "3775128e80c1da365aa8437eb7fc21ad",
						Value:      fileExceptToTypeInstance("complex-expect.json", "map").([]byte),
					},
				},
			},
			resultFilter) {
			t.Fatal("Not equal")
		}
	})
}

// initialProcessor 初始化预计算处理器
func initialProcessor(t *testing.T, dataId string, enabledMetrics bool) Processor {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Step1: initial metadataCenter
	center := NewMockMetaCenter(t, dataId)
	core.InitMetadataCenter(center)

	// Step2: initial storageBackend
	mockStorage := storage.NewMockBackend(ctrl)
	mockStorage.EXPECT().Exist(gomock.Any()).AnyTimes().Return(false, nil)

	return Processor{
		dataId:         dataId,
		config:         ProcessorOptions{metricReportEnabled: enabledMetrics},
		dataIdBaseInfo: core.BaseInfo{},
		proxy:          mockStorage,
		logger: monitorLogger.With(
			zap.String("location", "processor"),
			zap.String("dataId", dataId),
		),
		metricProcessor: newMetricProcessor(context.Background(), dataId, false),
		baseInfo:        core.GetMetadataCenter().GetBaseInfo(dataId),
	}
}

func runCase(
	p Processor, traceFileName string, exceptSaveRequests []storage.SaveRequest,
	filterResult func([]storage.SaveRequest) []storage.SaveRequest,
) bool {
	event := fileTracesToEvent(traceFileName)
	resultChan := make(chan storage.SaveRequest, 1000)
	p.PreProcess(resultChan, event)
	return assertResult(resultChan, len(resultChan), exceptSaveRequests, filterResult)
}

// assertResult 判断 处理结果 与 期望结果 是否一致
func assertResult(
	resultChan chan storage.SaveRequest,
	resultCount int,
	exceptSaveRequests []storage.SaveRequest,
	filterResult func([]storage.SaveRequest) []storage.SaveRequest,
) bool {
	c := 0
	var resultSaveRequests []storage.SaveRequest

	for {
		select {
		case item := <-resultChan:
			resultSaveRequests = append(resultSaveRequests, item)
			c++
			if c >= resultCount {
				resultSaveRequests = filterResult(resultSaveRequests)
				return assertSliceEqual(resultSaveRequests, exceptSaveRequests)
			}
		}
	}
}

// assertSliceEqual 判断两个SaveRequests列表是否相等
func assertSliceEqual(result, except []storage.SaveRequest) bool {
	if len(result) != len(except) {
		return false
	}

	sort.Slice(result, func(i, j int) bool {
		return reflect.DeepEqual(result[i], result[j])
	})

	sort.Slice(except, func(i, j int) bool {
		return reflect.DeepEqual(except[i], except[j])
	})

	for i := range result {

		resultData, ok := result[i].Data.(storage.EsStorageData)
		if ok {
			// 如果为 ES 保存数据 -> 判断字典是否相同
			resultMap := make(map[string]any)
			exceptMap := make(map[string]any)
			exceptData, _ := except[i].Data.(storage.EsStorageData)

			_ = json.Unmarshal(resultData.Value, &resultMap)
			_ = json.Unmarshal(exceptData.Value, &exceptMap)

			// 忽略入库时间
			resultMap["time"], exceptMap["time"] = 0, 0

			if !reflect.DeepEqual(resultMap, exceptMap) {
				return false
			}
		} else {
			if !reflect.DeepEqual(result[i], except[i]) {
				return false
			}
		}
	}

	return true
}

func mockConsulData(dataId string) []byte {
	info := map[string]any{
		"data_id":     dataId,
		"token":       dataId,
		"bk_biz_id":   2,
		"bk_biz_name": "BlueKing",
		"app_id":      1,
		"app_name":    "testApp",
		"kafka_info": map[string]string{
			"host":     "127.0.0.1",
			"username": "",
			"password": "",
			"topic":    "topic",
		},
		"trace_es_info": map[string]string{
			"index_name": "testIndexName",
			"host":       "127.0.0.1:9200",
			"username":   "",
			"password":   "",
		},
		"save_es_info": map[string]string{
			"index_name": "testIndexName",
			"host":       "127.0.0.1:9200",
			"username":   "",
			"password":   "",
		},
	}
	res, _ := json.Marshal(info)
	return res
}

// NewMockMetaCenter 创建一个 mock 的 consul 元数据获取
func NewMockMetaCenter(t *testing.T, dataId string) *core.MetadataCenter {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockStore := store.NewMockStore(ctrl)
	mockStore.EXPECT().Get(gomock.Any()).Return(mockConsulData(dataId), nil)

	centerInstance := &core.MetadataCenter{
		Mapping: &sync.Map{},
		Consul:  mockStore,
	}

	_ = centerInstance.AddDataId(dataId)
	return centerInstance
}
