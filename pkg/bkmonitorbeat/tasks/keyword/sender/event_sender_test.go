// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package sender_test

import (
	"context"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/sender"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test"
)

var pEventLinker chan interface{}

type mockEventPublisher struct {
	msgCount   int
	sendResult []common.MapStr
}

func (p *mockEventPublisher) Close() error {
	return nil
}

func (p *mockEventPublisher) PublishEvent(event common.MapStr) bool {
	p.msgCount++
	p.sendResult = append(p.sendResult, event)
	return true
}

func (p *mockEventPublisher) PublishEvents(events []common.MapStr) bool {
	p.msgCount += len(events)
	p.sendResult = append(p.sendResult, events...)
	return true
}

func (p mockEventPublisher) clean() {
	p.msgCount = 0
	p.sendResult = make([]common.MapStr, 0)
}

var (
	testDataID = 1000
	target     = "0:1.1.1.1"
)

func makeConfig() keyword.SendConfig {
	period, _ := time.ParseDuration("3s")
	config := keyword.SendConfig{
		DataID:       testDataID,
		Target:       target,
		ReportPeriod: period,
		OutputFormat: "event",
		TimeUnit:     "ms",
		Label: []configs.Label{
			{
				BkTargetTopoLevel: "nek_test",
				BkTargetTopoID:    "31",
			},
		},
		PackageCount: 50,
	}

	return config
}

// TestEventSenderBase: 基本功能测试，要求可以发送消息，验证消息中的维度符合预期
func TestEventSenderBase(t *testing.T) {
	// 初始化CMDB监控
	test.MakeWatcher()
	defer func() { test.CleanWatcher() }()

	// 初始化sender
	config := makeConfig()
	eventChan := make(chan define.Event, 10)
	ctx, ctxCancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "taskID", "IamTaskId123")
	s, _ := sender.New(ctx, config, eventChan)
	pEventLinker = make(chan interface{})
	s.AddInput(pEventLinker)
	_ = s.Start()

	// 发送一条测试消息
	for i := 0; i < 3; i++ {
		pEventLinker <- keyword.KeywordTaskResult{
			FilePath:     "test_path",
			RuleName:     "rule_one",
			SortedFields: []string{"field1", "field2"},
			Dimensions: map[string]string{
				"dimension_one": "one",
				"field1":        "f1",
				"field2":        "f2",
			},
			Log: "haha log", // 日志内容
		}
	}

	// 判断是否可以正常收到一条消息
	time.Sleep(5 * time.Second)
	ctxCancel()
	s.Wait()

	sendResult := make([]common.MapStr, 0)
Loop:
	for {
		select {
		case res := <-eventChan:
			sendResult = append(sendResult, res.AsMapStr())
		default:
			break Loop
		}
	}

	assert.Equal(t, len(sendResult), 1)
	assert.Equal(t, sendResult[0]["dataid"].(int), testDataID)

	// 判断拿到的结果是否符合预期
	sendContent := sendResult[0]["data"].([]common.MapStr)[0]
	// 检查维度
	expectDimension := map[string]interface{}{
		"dimension_one":      "one",
		"field1":             "f1",
		"field2":             "f2",
		"bk_biz_id":          "3",
		"bk_set_id":          "11",
		"bk_module_id":       "56",
		"bk_test_id":         "2",
		"bk_nek_test_id":     "31",
		"bk_target_ip":       "127.0.0.1",
		"bk_target_cloud_id": "0",
		"file_path":          "test_path",
	}
	for key, value := range expectDimension {
		actual := sendContent[sender.EventDimensionKey].(common.MapStr)[key]
		assert.Equalf(t, actual, value, "key %s", key)
	}

	// 检查计数及内容
	assert.Equal(t, sendContent["event"].(common.MapStr)["count"].(int), 3)
	assert.Equal(t, sendContent["event"].(common.MapStr)["content"].(string), "haha log")
	assert.Equal(t, sendContent["target"].(string), target)
	assert.Equal(t, sendContent["event_name"].(string), "rule_one")
}
