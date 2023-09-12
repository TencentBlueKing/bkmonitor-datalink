// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	FieldNameExtJSON = "ext_json"
)

// TransformAsIs : return value directly
func TransformAsIs(value interface{}) (interface{}, error) {
	return value, nil
}

// TransformContainer :
func TransformContainer(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case Container:
		return v, nil
	case map[string]interface{}:
		return NewMapContainerFrom(v), nil
	default:
		return nil, fmt.Errorf("unknown container type %T", value)
	}
}

// TransformMapBySeparator :
func TransformMapBySeparator(separator string, fields []string) TransformFn {
	count := len(fields)
	return func(from interface{}) (to interface{}, err error) {
		value, err := conv.DefaultConv.String(from)
		if err != nil {
			return nil, err
		}

		results := make(map[string]interface{}, count)
		var parts []string
		if value == "" {
			parts = []string{}
		} else {
			parts = strings.SplitN(value, separator, count)
		}
		total := len(parts)
		for i, name := range fields {
			if i < total {
				results[name] = parts[i]
			} else {
				results[name] = nil
			}
		}
		return results, nil
	}
}

// TransformMapByRegexp
func TransformMapByRegexp(pattern string) TransformFn {
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return TransformErrorForever(err)
	}

	fields := regex.SubexpNames()
	count := len(fields)
	return func(from interface{}) (to interface{}, err error) {
		value, err := conv.DefaultConv.String(from)
		if err != nil {
			return nil, err
		}

		results := make(map[string]interface{}, count)
		matched := regex.FindStringSubmatch(value)
		matchedCount := len(matched)
		for i := 1; i < count; i++ {
			fieldName := fields[i]
			if len(fieldName) == 0 {
				// 去掉未命名的字段
				continue
			}
			if i < matchedCount {
				results[fieldName] = matched[i]
			} else {
				results[fieldName] = nil
			}
		}

		return results, nil
	}
}

func TransformMapByJsonWithRetainExtraJSON(table *config.MetaResultTableConfig) TransformFn {
	options := utils.NewMapHelper(table.Option)
	retainExtraJSON, _ := options.GetBool(config.PipelineConfigOptionRetainExtraJson)
	userFieldMap := table.FieldListGroupByName()
	return func(from interface{}) (to interface{}, err error) {
		value, err := conv.DefaultConv.String(from)
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		results := make(map[string]interface{})
		err = json.Unmarshal([]byte(value), &results)
		if err != nil {
			return nil, err
		}
		if retainExtraJSON {
			extraJSONMap := make(map[string]interface{})
			for key, value := range results {
				if _, ok := userFieldMap[key]; !ok {
					extraJSONMap[key] = value
				}
			}
			results[FieldNameExtJSON] = extraJSONMap
		}
		return results, nil
	}
}

// TransformMapByJSON
func TransformMapByJSON(from interface{}) (to interface{}, err error) {
	value, err := conv.DefaultConv.String(from)
	if err != nil {
		return nil, err
	}

	if value == "" {
		return nil, nil
	}

	results := make(map[string]interface{})
	err = json.Unmarshal([]byte(value), &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// TransformInterfaceByJSONString
func TransformInterfaceByJSONString(from string) (to interface{}, err error) {
	var result interface{}
	err = json.Unmarshal([]byte(from), &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// TransformJSON : return value as json
func TransformJSON(value interface{}) (interface{}, error) {
	j, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return conv.DefaultConv.String(j)
}

// TransformChain :
func TransformChain(transformers ...func(interface{}) (interface{}, error)) func(interface{}) (interface{}, error) {
	return func(value interface{}) (interface{}, error) {
		var err error
		for _, transformer := range transformers {
			value, err = transformer(value)
			if err != nil {
				return nil, err
			}
		}
		return value, nil
	}
}

// TransformErrorForever :
func TransformErrorForever(err error) TransformFn {
	return func(from interface{}) (interface{}, error) {
		return nil, err
	}
}

func TransformObject(from interface{}) (to interface{}, err error) {
	switch i := from.(type) {
	case string:
		if len(i) == 0 {
			return map[string]interface{}{}, nil
		}
		return TransformMapByJSON(i)
	case MapContainer, map[string]interface{}:
		return i, nil
	default:
		logging.Warnf("the type->[%T] dose no support convert to object", from)
		return map[string]interface{}{}, nil
	}
}

func TransformNested(from interface{}) (to interface{}, err error) {
	switch i := from.(type) {
	case string:
		if len(i) == 0 {
			return []interface{}{}, nil
		}
		return TransformInterfaceByJSONString(from.(string))
	case map[string]interface{}:
		return i, nil
	case []interface{}:
		return i, nil
	default:
		logging.Warnf("the type->[%T] dose no support convert to nested", from)
		return []interface{}{}, nil
	}
}

// NewTransformByType :
func NewTransformByType(name define.MetaFieldType) TransformFn {
	switch name {
	case define.MetaFieldTypeInt:
		return TransformNilInt64
	case define.MetaFieldTypeUint:
		return TransformNilUint64
	case define.MetaFieldTypeFloat:
		return TransformNilFloat64
	case define.MetaFieldTypeString:
		return TransformNilString
	case define.MetaFieldTypeBool:
		return TransformNilBool
	case define.MetaFieldTypeTimestamp:
		return TransformAutoTimeStamp
	case define.MetaFieldTypeObject:
		return TransformObject
	case define.MetaFieldTypeNested:
		return TransformNested
	default:
		return TransformAsIs
	}
}

// NewTransformByField :
func NewTransformByField(field *config.MetaFieldConfig) TransformFn {
	switch field.Type {
	case define.MetaFieldTypeTimestamp:
		options := utils.NewMapHelper(field.Option)
		if options.Exists(config.MetaFieldOptTimeFormat) {
			return TransformTimeStampByName(
				options.MustGetString(config.MetaFieldOptTimeFormat),
				conv.Int(options.GetOrDefault(config.MetaFieldOptTimeZone, 0)),
			)
		}
		fallthrough
	default:
		return NewTransformByType(field.Type)
	}
}
