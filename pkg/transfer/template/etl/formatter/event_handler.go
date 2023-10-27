// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter

import (
	"fmt"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// CleanElements 校验事件记录的各个字段是否符合预期, 可以支持event及dimension具体内容的查询
// allowEmpty 是否允许为空;allowNewElement表示是否可以接受新的元素; elements表示当前已经存在元素内容; data则是需要处理的内容
// 在事件下，元素则是指新的事件字段（默认只有event_content等）；在维度下，元素则是指新的维度字段
// 如果不允许新的元素的情况下，会将未被允许的元素进行删除
func CleanElements(allowEmpty, allowNewElement bool, elements map[string]interface{}, data map[string]interface{}) error {
	if !allowEmpty && len(data) == 0 {
		logging.Debugf("allowEmpty is false and data is empty, error will return")
		return errors.Wrapf(define.ErrValue, "data is empty")
	}

	// 如果不关注新的元素，直接可以返回即可
	if allowNewElement {
		logging.Debugf("allowNewElement is true, no data will bo check, all allowed.")
		return nil
	}

	// 否则，需要关注具体的元素是否已经存在
	for elementName := range data {
		// 判断该元素是否存在
		if _, ok := elements[elementName]; !ok {
			// 如果不存在的，需要将该元素删除
			delete(data, elementName)
			logging.Debugf("allowNewElement is false and element %s is delete from data", elementName)
		}
	}

	return nil
}

// cleanEventContent 检查event的元素内容
func CleanEventContent(allowNewEventContent bool, eventContent map[string]interface{}) define.ETLRecordChainingHandler {
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		event, ok := record.Metrics["event"]
		if !ok {
			return errors.Wrapf(define.ErrValue, "event field not found in metrics")
		}
		eventData, ok := event.(map[string]interface{})
		if !ok {
			return errors.Wrapf(define.ErrType, fmt.Sprintf("metric.event should be map, event: %+v", event))
		}
		if err := CleanElements(false, allowNewEventContent, eventContent, eventData); err != nil {
			return err
		}
		return next(record)
	}
}

// CleanEventDimensions 检查event的维度内容
// originDimensionMap内容为ResultTableOptEventDimensionList的格式
func CleanEventDimensions(allowNewDimension bool, originDimensionMap map[string][]string, commonDimensions []string) define.ETLRecordChainingHandler {
	dimensionMap := make(map[string]map[string]interface{})

	// 遍历构造一个map的结构，方便可以快速命中
	for eventName, dimensionListIf := range originDimensionMap {
		tempDimensionMap := make(map[string]interface{})
		// 追加事件自己的维度
		dimensionList := make([]string, 0)
		if err := mapstructure.Decode(dimensionListIf, &dimensionList); err != nil {
			logging.Fatalf("decode originDimensionMap failed, err: %+v", err)
		}
		for _, dimension := range dimensionList {
			tempDimensionMap[dimension] = struct{}{}
		}
		// 追加公共的维度，防止被清理
		for _, dimension := range commonDimensions {
			tempDimensionMap[dimension] = struct{}{}
		}
		dimensionMap[eventName] = tempDimensionMap
	}

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		// 需要根据每个事件，获取当前时间可以有的维度
		var ok bool
		var elements map[string]interface{}

		if elements, ok = dimensionMap[record.Dimensions["event_name"].(string)]; !ok {
			// 如果没有命中，则提供一个新的空维度map使用
			elements = make(map[string]interface{})
		}

		if err := CleanElements(false /* allowEmpty */, allowNewDimension, elements, record.Dimensions); err != nil {
			return err
		}
		return next(record)
	}
}

// checkElement: 判断一个元素是否存在与数据当中，如果不存在的，则会抛出错误
func CheckElement(elements []string, data map[string]interface{}) error {
	for _, elementName := range elements {
		if _, ok := data[elementName]; !ok {
			logging.Debugf("failed to get element->[%s] from data->[%v], error will raise.", elementName, data)
			return errors.Wrapf(define.ErrValue, "element[%s] is missing", elementName)
		}
	}

	return nil
}

// checkEventCommon: 检查默认内置的字段内容, 此处将elements是通过参数传入，主要是考虑后续可以通过metadata的配置进行调整
// 此处的公共内容，通常是指内置必须存在的字段或者维度，例如，event下的event_content，dimension下的target
func CheckEventCommonDimensions(eventElements, dimensionElements []string) define.ETLRecordChainingHandler {
	// default field inject into dimensions
	dimensionElements = append(dimensionElements, define.RecordEventTargetName)
	dimensionElements = append(dimensionElements, define.RecordEventEventNameName)

	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		event, ok := record.Metrics["event"]
		if !ok {
			return errors.Wrapf(define.ErrValue, "metrics should have event field")
		}
		eventData, ok := event.(map[string]interface{})
		if !ok {
			return errors.Wrapf(define.ErrType, fmt.Sprintf("metrics.event should be map, but got: %+v", event))
		}
		if err := CheckElement(eventElements, eventData); err != nil {
			return err
		}

		if err := CheckElement(dimensionElements, record.Dimensions); err != nil {
			return err
		}

		return next(record)
	}
}

// CleanElementTypes: 将elements中的对应value都转换为string或者float64
// 这里只会对自定义的上报内容做这样的处理，维度统一转换为string
func CleanElementTypes(record *define.ETLRecord, next define.ETLRecordHandler) error {
	for name, value := range record.Dimensions {
		if name == "dimensions" {
			// EventDimensions 通过 event-handler 注入
			dimensions, ok := value.(map[string]interface{})
			if !ok {
				return errors.Wrapf(define.ErrType, "dimensions should be map")
			}
			for name, value := range dimensions {
				val, err := assertValueType(name, value)
				if err != nil {
					logging.Errorf("failed to detect value: %v from float or string, something go wrong?", value)
					return err
				}
				dimensions[name] = val
			}
			record.Dimensions["dimensions"] = dimensions
			continue
		}
		val, err := assertValueType(name, value)
		if err != nil {
			logging.Errorf("failed to detect value: %v from float or string, something go wrong?", value)
			return err
		}
		record.Dimensions[name] = val
	}
	return next(record)
}

func assertValueType(name string, value interface{}) (result interface{}, err error) {
	result = value
	switch value.(type) {
	// 由于通过了flat-batch的处理，因此现在类型只有float64和string两种
	case float64:
		result = conv.String(value)
	case string:
	default:
		return nil, errors.Wrapf(define.ErrType, fmt.Sprintf("unsupported type, key: %s, value: %+v", name, value))
	}
	return result, nil
}

// CheckTimestampPrecision check timestamp precision, default is ms
func CheckTimestampPrecision(timestampPrecision string) define.ETLRecordChainingHandler {
	return func(record *define.ETLRecord, next define.ETLRecordHandler) error {
		if record.Time == nil {
			return next(record)
		}

		if err := CheckTimestamp(timestampPrecision, *record.Time); err != nil {
			return err
		}

		return next(record)
	}
}

// checkElement: 判断一个元素是否存在与数据当中，如果不存在的，则会抛出错误
func CheckTimestamp(precision string, ts int64) error {
	var specDuration time.Duration
	switch precision {
	case "s":
		specDuration = time.Second
	case "ms":
		specDuration = time.Millisecond
	case "us":
		specDuration = time.Microsecond
	case "ns":
		specDuration = time.Nanosecond
	default:
		return fmt.Errorf("invalid timestamp precision config: %s", precision)
	}

	duration := utils.RecognizeTimeStampPrecision(ts)
	if specDuration != duration {
		msg := fmt.Sprintf("record time precision [%s] conflict with configuration: %s", duration.String(), precision)
		logging.Warn(msg)
		return errors.Wrapf(define.ErrValue, msg)
	}

	return nil
}
