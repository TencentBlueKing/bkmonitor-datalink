// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxyvalidator

import (
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
)

const (
	configVersion = "v2"
)

// Validator proxy 数据校验器定义
type Validator interface {
	Validate(*define.ProxyData) error
}

type noneValidator struct{}

func (noneValidator) Validate(*define.ProxyData) error {
	return errors.New("unsupported validator")
}

type nameValidator struct{}

func (nv *nameValidator) Validate(s string) error {
	if !utils.IsNameNormalized(s) {
		return errors.Errorf("name '%s' required match regex [^[a-zA-Z_][a-zA-Z0-9_]*$]", s)
	}
	return nil
}

type dimensionValidator struct {
	nameValidated bool
	nameValidator *nameValidator
}

func (dv *dimensionValidator) Validate(objs map[string]interface{}) (map[string]interface{}, error) {
	dimensionObj, ok := objs["dimension"]
	if !ok {
		return map[string]interface{}{}, nil
	}

	dimension, ok := dimensionObj.(map[string]interface{})
	if !ok {
		return nil, errors.Errorf("dimension expected map[string]interface{} type, got %T", dimensionObj)
	}

	conv := make(map[string]string)
	for name, valObj := range dimension {
		if dv.nameValidated {
			if err := dv.nameValidator.Validate(name); err != nil {
				return nil, err
			}
		}

		// 空对象删除
		if valObj == nil {
			delete(dimension, name)
			continue
		}

		switch valObj.(type) {
		case string: // pass
		case float64, bool: // 尝试转换为 string
			conv[name] = cast.ToString(valObj)
		default:
			return nil, errors.Errorf("dimension '%s' value expected string type, got %T", name, valObj)
		}
	}

	// 替换 string 值
	for k, v := range conv {
		dimension[k] = v
	}

	return dimension, nil
}

type timestampValidator struct {
	offset int64
}

func (tv *timestampValidator) Validate(objs map[string]interface{}) (float64, error) {
	timestampObj, ok := objs["timestamp"]
	if !ok {
		return float64(time.Now().UnixMilli()), nil
	}

	timestamp, ok := timestampObj.(float64)
	if !ok {
		return 0, errors.Errorf("timestamp expected float64 type, got %T", timestampObj)
	}

	// 上报使用 0 时间戳则使用服务器当前时间
	if timestamp == 0 {
		return float64(time.Now().UnixMilli()), nil
	}

	now := time.Now().Unix()
	unixTimestamp := timestamp / 1000
	if int64(unixTimestamp)-now > tv.offset {
		return 0, errors.Errorf("reject future timestamp, dataTs=%v, serverTs=%v, offset=%v(s)", int(unixTimestamp), now, tv.offset)
	}
	return timestamp, nil
}

// NewValidator 返回校验器实例 内置 TimeSeries Validator 以及 Event Validator
func NewValidator(config Config) Validator {
	if config.Version != configVersion {
		return noneValidator{}
	}

	switch config.Type {
	case dataTypeTimeSeries:
		return newTimeSeriesValidator(config)
	case dataTypeEvent:
		return newEventValidator(config)
	default:
		return noneValidator{}
	}
}

// TimeSeries Validator

type timeSeriesValidator struct {
	nameValidator      *nameValidator
	dimensionValidator *dimensionValidator
	timestampValidator *timestampValidator
}

func newTimeSeriesValidator(config Config) Validator {
	nv := &nameValidator{}
	return &timeSeriesValidator{
		nameValidator:      nv,
		dimensionValidator: &dimensionValidator{nameValidator: nv, nameValidated: true},
		timestampValidator: &timestampValidator{offset: config.MaxFutureTimeOffset},
	}
}

func (tc *timeSeriesValidator) Validate(pd *define.ProxyData) error {
	objs, ok := pd.Data.([]interface{})
	if !ok {
		return errors.Errorf("timeseries data expected []interface{}, got %T", pd.Data)
	}
	if len(objs) == 0 {
		return errors.New("timeseries data cannot be empty")
	}

	for _, obj := range objs {
		mapObj, ok := obj.(map[string]interface{})
		if !ok {
			return errors.Errorf("timeseries each item expected map[string]interface{} type, got %T", obj)
		}

		// 校验 metrics 字段
		metricsObj, ok := mapObj["metrics"]
		if !ok {
			return errors.New("metrics missing")
		}
		metrics, ok := metricsObj.(map[string]interface{})
		if !ok {
			return errors.Errorf("metrics expected map[string]interface{} type, got %T", metricsObj)
		}
		if len(metrics) == 0 {
			return errors.New("metrics cannot be empty")
		}

		for name, value := range metrics {
			if err := tc.nameValidator.Validate(name); err != nil {
				return err
			}

			_, ok := value.(float64)
			if !ok {
				return errors.Errorf("value expected float64 type, got %T", value)
			}
		}

		dimension, err := tc.dimensionValidator.Validate(mapObj)
		if err != nil {
			return err
		}
		mapObj["dimension"] = dimension

		timestamp, err := tc.timestampValidator.Validate(mapObj)
		if err != nil {
			return err
		}
		mapObj["timestamp"] = int64(timestamp)
	}

	pd.Type = define.ProxyMetricType
	return nil
}

// Event Validator

type eventValidator struct {
	dimensionValidator *dimensionValidator
	timestampValidator *timestampValidator
}

func newEventValidator(config Config) Validator {
	nv := &nameValidator{}
	return &eventValidator{
		dimensionValidator: &dimensionValidator{nameValidator: nv, nameValidated: false},
		timestampValidator: &timestampValidator{offset: config.MaxFutureTimeOffset},
	}
}

func (tc *eventValidator) Validate(pd *define.ProxyData) error {
	objs, ok := pd.Data.([]interface{})
	if !ok {
		return errors.Errorf("event data expected []interface{}, got %T", pd.Data)
	}
	if len(objs) == 0 {
		return errors.New("event data cannot be empty")
	}

	for _, obj := range objs {
		mapObj, ok := obj.(map[string]interface{})
		if !ok {
			return errors.Errorf("event each item expected map[string]interface{} type, got %T", obj)
		}

		// 校验 target 字段
		targetObj, ok := mapObj["target"]
		if !ok {
			return errors.New("target missing")
		}
		target, ok := targetObj.(string)
		if !ok {
			return errors.Errorf("target expected string type, got %T", targetObj)
		}
		if len(target) == 0 {
			return errors.New("target cannot be empty")
		}

		// 校验 event_name 字段
		eventNameObj, ok := mapObj["event_name"]
		if !ok {
			return errors.New("event_name missing")
		}
		_, ok = eventNameObj.(string)
		if !ok {
			return errors.Errorf("eventName expected string type, got %T", eventNameObj)
		}

		// 校验 event 字段
		eventObj, ok := mapObj["event"]
		if !ok {
			return errors.New("event missing")
		}
		event, ok := eventObj.(map[string]interface{})
		if !ok {
			return errors.Errorf("event expected map[string]interface{} type, got %T", eventObj)
		}
		if _, ok = event["content"]; !ok {
			return errors.New("event.content missing")
		}

		dimension, err := tc.dimensionValidator.Validate(mapObj)
		if err != nil {
			return err
		}
		mapObj["dimension"] = dimension

		timestamp, err := tc.timestampValidator.Validate(mapObj)
		if err != nil {
			return err
		}
		mapObj["timestamp"] = int64(timestamp)
	}

	pd.Type = define.ProxyEventType
	return nil
}
