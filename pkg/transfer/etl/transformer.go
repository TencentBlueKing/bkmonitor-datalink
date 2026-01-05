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
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/types"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	FieldExtJSON          = "ext_json"
	FieldRetainContentKey = "log"
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

		var failed bool
		total := len(parts)
		for i, name := range fields {
			if i < total {
				results[name] = strings.TrimSpace(parts[i])
			} else {
				results[name] = nil
				failed = true
			}
		}

		results[config.LogCleanFailedFlag] = failed
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

		var failed bool
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
				failed = true
			}
		}

		results[config.LogCleanFailedFlag] = failed
		return results, nil
	}
}

func TransformMapByJsonWithRetainExtraJSON(table *config.MetaResultTableConfig) TransformFn {
	options := utils.NewMapHelper(table.Option)
	retainExtraJSON, _ := options.GetBool(config.PipelineConfigOptionRetainExtraJson)
	enableRetainContent, _ := options.GetBool(config.PipelineConfigOptionRetainContent)
	retainContentKey, rkExist := options.GetString(config.PipelineConfigOptionRetainContentKey)

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
			if enableRetainContent {
				rk := FieldRetainContentKey
				if rkExist && retainContentKey != "" {
					rk = retainContentKey
				}
				return map[string]interface{}{
					rk:                        value,
					config.LogCleanFailedFlag: true,
				}, nil
			}
			return nil, err
		}

		if retainExtraJSON {
			extraJSONMap := make(map[string]interface{})
			for key, value := range results {
				if _, ok := userFieldMap[key]; !ok {
					extraJSONMap[key] = value
				}
			}
			results[FieldExtJSON] = extraJSONMap
		}
		results[config.LogCleanFailedFlag] = false
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

type DbmRequest struct {
	Content string `json:"content" binding:"required"`
}

type DbmResponse struct {
	Command         string `json:"command"`
	QueryString     string `json:"query_string"`
	QueryDigestText string `json:"query_digest_text"`
	QueryDigestMd5  string `json:"query_digest_md5"`
	DbName          string `json:"db_name"`
	TableName       string `json:"table_name"`
	QueryLength     int    `json:"query_length"`
}

// ParseDbmSlowQuery 解析 sql 语句
func ParseDbmSlowQuery(url, content string, retry int) (*DbmResponse, error) {
	var resp *DbmResponse
	var err error

	delay := time.Millisecond * 100 // 初始重试 delay 100ms 重试时成倍增加
	for i := 0; i <= retry; i++ {
		resp, err = parseDbmSlowQuery(url, content)
		if err != nil {
			logging.MinuteErrorfSampling("DbmSlowQuery", "failed to request slow query, content=[%s], err: %v", content, err)
			time.Sleep(delay)
			delay = delay * 2
			continue
		}
		return resp, nil // 请求成功 返回结果
	}
	return nil, err
}

var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: time.Minute,
		}).DialContext,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     2 * time.Minute,
	},
}

func parseDbmSlowQuery(url, content string) (*DbmResponse, error) {
	req := DbmRequest{Content: content}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	buf := bytes.NewBuffer(body)
	resp, err := httpClient.Post(url, "", buf)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var dbmResponse DbmResponse
	if err := json.Unmarshal(b, &dbmResponse); err != nil {
		return nil, err
	}
	return &dbmResponse, nil
}

type DbmRecord struct {
	ResponseFieldName string
	Response          DbmResponse
	BodyFieldName     string
	Body              string
}

// NewTransformByField :
func NewTransformByField(field *config.MetaFieldConfig, rt *config.MetaResultTableConfig) TransformFn {
	options := utils.NewMapHelper(field.Option)

	// dbm_* 代表数据来源自 dbm 需要解析
	dbmEnabled, _ := options.GetBool(config.MetaFieldOptDbmEnabled)
	dbmUrl, _ := options.GetString(config.MetaFieldOptDbmUrl)
	dbmField, _ := options.GetString(config.MetaFieldOptDbmField)
	dbmRetry, _ := options.GetInt(config.MetaFieldOptDbmRetry)

	// 将 from 当做 dbm 数据来处理必要要求，如若不符合以下条件则当做普通字符串处理
	// 1) dbm_enabled 字段指定是否启动慢查询处理
	// 2) dbm_url 不为空 即请求解析 sql 的地址
	// 3) dbm_field 解析后的数据写到的字段不为空
	// 4) dbm_retry 失败重试次数
	if field.Type == define.MetaFieldTypeString && dbmEnabled && dbmUrl != "" && dbmField != "" {
		return func(from interface{}) (interface{}, error) {
			obj, err := TransformNilString(from)
			if err != nil {
				return nil, err
			}
			s, ok := obj.(string)
			if !ok {
				return nil, err
			}
			resp, err := ParseDbmSlowQuery(dbmUrl, s, dbmRetry)
			if err != nil {
				return nil, err
			}

			return DbmRecord{
				Response:          *resp,
				ResponseFieldName: dbmField,
				Body:              s,
				BodyFieldName:     field.FieldName,
			}, nil
		}
	}

	// 保留原始字符串 而不是 golang 数据模型
	// map[string]string{} => {"foo":"bar"}
	var originString bool
	if rt != nil {
		rtOpt := utils.NewMapHelper(rt.Option)
		originString, _ = rtOpt.GetBool(config.MataFieldOptEnableOriginString)
	}

	if field.Type == define.MetaFieldTypeString && originString {
		return func(from interface{}) (interface{}, error) {
			if from == nil {
				return nil, nil
			}

			var matched bool
			switch from.(type) {
			case map[string]interface{}:
				matched = true
			case []interface{}:
				matched = true
			}

			if matched {
				txt, err := json.Marshal(from)
				if err == nil {
					return string(txt), nil
				}
			}

			// 退避处理
			result, err := conv.DefaultConv.String(from)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}

	switch field.Type {
	case define.MetaFieldTypeTimestamp:
		if options.Exists(config.MetaFieldOptTimeFormat) {
			return func(from interface{}) (to interface{}, err error) {
				format := options.MustGetString(config.MetaFieldOptTimeFormat)

				layout, ok := options.GetString(config.MetaFieldOptTimeLayout)
				if ok && len(layout) > 0 && len(format) > 0 {
					define.RegisterTimeLayout(format, layout)
				}

				fn := TransformTimeStampByName(format, conv.Int(options.GetOrDefault(config.MetaFieldOptTimeZone, 0)))
				result, err := fn(from)
				if err != nil {
					return result, err
				}
				if result == nil {
					return nil, nil
				}

				v, ok := field.Option[config.MetaFieldOptTimestampUnit]
				if !ok {
					return result, nil
				}
				u, ok := v.(string)
				if !ok {
					return result, nil
				}

				ts, ok := result.(types.TimeStamp)
				if !ok {
					return result, nil
				}
				(&ts).SetUnit(u)
				return ts, err
			}
		}
		fallthrough
	default:
		return NewTransformByType(field.Type)
	}
}
