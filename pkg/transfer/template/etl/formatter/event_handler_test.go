// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type EventHandlerTest struct {
	testsuite.ETLSuite
}

// TestCleanElements: 测试清理元素的能力是否符合预期
func (s *EventHandlerTest) TestCleanElements() {
	var err error
	// 提供空的元素内容，且不允许为空
	err = formatter.CleanElements(false, false, map[string]interface{}{}, map[string]interface{}{})
	s.NotEqual(nil, err)

	// 提供空的元素内容，且允许为空
	err = formatter.CleanElements(true, false, map[string]interface{}{}, map[string]interface{}{})
	s.Equal(nil, err)

	// 提供非空的元素内容，不允许出现新的元素，检查是否正常清理
	elementsMap := map[string]interface{}{
		"dimension1": struct{}{},
	}
	checkMap1 := map[string]interface{}{
		"dimension1": "value",
		"dimension2": "value",
	}
	err = formatter.CleanElements(false, false, elementsMap, checkMap1)
	s.EqualValues(checkMap1, map[string]interface{}{
		"dimension1": "value",
	})

	// 提供非空的元素，允许新的元素，检查是否正常保留
	checkMap2 := map[string]interface{}{
		"dimension1": "value",
		"dimension2": "value",
	}
	err = formatter.CleanElements(false, true, elementsMap, checkMap2)
	s.EqualValues(checkMap2, map[string]interface{}{
		"dimension1": "value",
		"dimension2": "value",
	})
}

// TestCheckElement: 测试元素内容检查能力是否符合预期
func (s *EventHandlerTest) TestCheckElement() {
	var err error
	standardElements := []string{
		"target", "event_content",
	}

	// 提供刚刚好的元素，判断是否可以正常返回
	err = formatter.CheckElement(standardElements, map[string]interface{}{
		"target":        "value",
		"event_content": "value",
	})
	s.Equal(nil, err)

	// 提供一个缺少的元素，判断是否可以判断异常
	err = formatter.CheckElement(standardElements, map[string]interface{}{
		"target": "value",
	})
	s.NotEqual(nil, err)

	// 提供一个元素较数据少的情况，判断是否可以正常返回
	err = formatter.CheckElement(standardElements, map[string]interface{}{
		"target":        "value",
		"event_content": "value",
		"dimension1":    "value",
	})
	s.Equal(nil, err)
}

// TestCleanEventDimensions: 测试Dimension清理能力
func (s *EventHandlerTest) TestCleanEventDimensions() {
	var handler define.ETLRecordChainingHandler
	DimensionMap := map[string][]string{
		"event_name": {"dimension1", "dimension2"},
	}
	commonDimensions := []string{"event_name"}
	data := &define.ETLRecord{
		Dimensions: map[string]interface{}{
			"event_name": "event_name",
			"dimension1": struct{}{},
			"dimension2": struct{}{},
			"dimension3": struct{}{},
		},
	}

	// 测试允许存在新维度的情况
	handler = formatter.CleanEventDimensions(true, DimensionMap, commonDimensions)
	_ = handler(data, func(record *define.ETLRecord) error {
		s.Equal(record.Dimensions, map[string]interface{}{
			"event_name": "event_name",
			"dimension1": struct{}{},
			"dimension2": struct{}{},
			"dimension3": struct{}{},
		})
		return nil
	})

	// 测试事件是全新的情况
	handler = formatter.CleanEventDimensions(true, DimensionMap, commonDimensions)
	_ = handler(&define.ETLRecord{
		Dimensions: map[string]interface{}{
			"event_name": "event_new",
			"dimension1": struct{}{},
			"dimension2": struct{}{},
			"dimension3": struct{}{},
		},
	}, func(record *define.ETLRecord) error {
		s.Equal(record.Dimensions, map[string]interface{}{
			"event_name": "event_new",
			"dimension1": struct{}{},
			"dimension2": struct{}{},
			"dimension3": struct{}{},
		})
		return nil
	})

	// 测试不允许存在新维度的情况
	handler = formatter.CleanEventDimensions(false, DimensionMap, commonDimensions)
	_ = handler(data, func(record *define.ETLRecord) error {
		s.Equal(record.Dimensions, map[string]interface{}{
			"event_name": "event_name",
			"dimension1": struct{}{},
			"dimension2": struct{}{},
		})
		return nil
	})
}

// TestCleanEventDimensions: 测试Event的清理能力
func (s *EventHandlerTest) TestCleanEventContent() {
	var handler define.ETLRecordChainingHandler
	contentList := map[string]interface{}{
		"event_content": struct{}{},
	}

	data := &define.ETLRecord{
		Metrics: map[string]interface{}{
			"event_content": "haha",
			"event_count":   123,
		},
	}
	// 测试允许增加自定义内容的时候
	handler = formatter.CleanEventContent(true, contentList)
	_ = handler(data, func(record *define.ETLRecord) error {
		s.Equal(record.Metrics, map[string]interface{}{
			"event_content": "haha",
			"event_count":   123,
		})
		return nil
	})

	// 测试允许禁止自定义内容的时候
	handler = formatter.CleanEventContent(false, contentList)
	_ = handler(data, func(record *define.ETLRecord) error {
		s.Equal(record.Metrics, map[string]interface{}{
			"event_content": "haha",
		})
		return nil
	})
}

// CheckEventCommonDimensions: 测试检查公共Dimension和Event的内容
func (s *EventHandlerTest) TestCheckEventCommonDimensions() {
	var err error
	dimensionElement := []string{"target"}
	eventElement := []string{"event_content"}

	// 测试正常的情况
	handler := formatter.CheckEventCommonDimensions(eventElement, dimensionElement)
	err = handler(&define.ETLRecord{
		Metrics: map[string]interface{}{
			"event": map[string]interface{}{
				"event_content": "123",
			},
		},
		Dimensions: map[string]interface{}{
			"target":     "123",
			"event_name": "test",
		},
	}, s.EmptyHandler)
	s.Equal(nil, err)

	// 增加一个新的维度，会导致报错
	dimensionElement = []string{"target", "new_target"}
	handler = formatter.CheckEventCommonDimensions(eventElement, dimensionElement)
	err = handler(&define.ETLRecord{
		Metrics: map[string]interface{}{
			"event_content": "123",
		},
		Dimensions: map[string]interface{}{
			"target": "123",
		},
	}, s.EmptyHandler)
	s.NotEqual(nil, err)
}

// EventHandlerTest
func TestEventHandlers(t *testing.T) {
	suite.Run(t, new(EventHandlerTest))
}
