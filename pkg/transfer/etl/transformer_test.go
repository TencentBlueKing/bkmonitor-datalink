// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl_test

import (
	"errors"
	"testing"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/types"
)

// TransformSuite :
type TransformSuite struct {
	suite.Suite
}

// TestUsage :
func (s *TransformSuite) TestUsage() {
	cases := []interface{}{
		0, nil, "string", 1.0, make(map[string]interface{}), errors.New("test"),
	}

	for _, c := range cases {
		value, err := etl.TransformAsIs(c)
		s.Equal(c, value)
		s.NoError(err)
	}
}

// TestTransform :
func TestTransform(t *testing.T) {
	suite.Run(t, new(TransformSuite))
}

// TransformByFieldSuite :
type TransformByFieldSuite struct {
	suite.Suite
}

// TestUsage :
func (s *TransformByFieldSuite) TestUsage() {
	cases := []struct {
		field    config.MetaFieldConfig
		value    interface{}
		excepted interface{}
	}{
		{config.MetaFieldConfig{Type: define.MetaFieldTypeString}, 1, "1"},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeString}, nil, nil},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeInt}, "1", int64(1)},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeInt}, nil, nil},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeUint}, "1", uint64(1)},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeUint}, nil, nil},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeFloat}, "1", 1.0},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeFloat}, nil, nil},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeBool}, "1", true},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeBool}, nil, nil},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeBool}, "1", true},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeBool}, nil, nil},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeObject}, ``, map[string]interface{}{}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeObject}, nil, map[string]interface{}{}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeObject}, `{"a": "12", "b": "23"}`, map[string]interface{}{"a": "12", "b": "23"}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeObject}, `{"a": "12", "b": {"c": "23"}}`, map[string]interface{}{"a": "12", "b": map[string]interface{}{"c": "23"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeObject}, map[string]interface{}{"a": "12", "b": "23"}, map[string]interface{}{"a": "12", "b": "23"}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeObject}, map[string]interface{}{"a": "12", "b": map[string]interface{}{"c": "23"}}, map[string]interface{}{"a": "12", "b": map[string]interface{}{"c": "23"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, map[string]interface{}{"a": "12", "b": map[string]interface{}{"c": "23"}}, map[string]interface{}{"a": "12", "b": map[string]interface{}{"c": "23"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, map[string]interface{}{"a": []string{"xxx", "xxx"}, "b": map[string]interface{}{"c": "23"}}, map[string]interface{}{"a": []string{"xxx", "xxx"}, "b": map[string]interface{}{"c": "23"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, []interface{}{"a", "b"}, []interface{}{"a", "b"}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, []interface{}{map[string]interface{}{"a": "b"}}, []interface{}{map[string]interface{}{"a": "b"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, `{"a": ["xxx"], "b": {"a":  "a"}}`, map[string]interface{}{"a": []interface{}{"xxx"}, "b": map[string]interface{}{"a": "a"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, `[{"a": "a", "b": "b"}, {"b": "b"}]`, []interface{}{map[string]interface{}{"a": "a", "b": "b"}, map[string]interface{}{"b": "b"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, `[{"a": [{"a": "b"}], "b": "b"}, {"b": "b"}]`, []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": "b"}}, "b": "b"}, map[string]interface{}{"b": "b"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, `[{"a": [{"a": [{"a": "a"}]}], "b": "b"}, {"b": "b"}]`, []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": "a"}}}}, "b": "b"}, map[string]interface{}{"b": "b"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, `[{"a": [{"a": [{"a": 123}]}], "b": "b"}, {"b": "b"}]`, []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": float64(123)}}}}, "b": "b"}, map[string]interface{}{"b": "b"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, `[{"a": [{"a": [{"a": true}]}], "b": "b"}, {"b": "b"}]`, []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": []interface{}{map[string]interface{}{"a": true}}}}, "b": "b"}, map[string]interface{}{"b": "b"}}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, ``, []interface{}{}},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeNested}, nil, []interface{}{}},
	}

	for i, c := range cases {
		fn := etl.NewTransformByField(&c.field)
		result, err := fn(c.value)
		s.NoError(err, i)
		s.Equal(c.excepted, result)
	}
}

// TestTimeStamp :
func (s *TransformByFieldSuite) TestTimeStamp() {
	now := time.Now()
	_, offset := now.Zone()
	zone := offset / 3600
	cases := []struct {
		field     config.MetaFieldConfig
		value     interface{}
		timestamp int64
	}{
		{config.MetaFieldConfig{Type: define.MetaFieldTypeTimestamp}, now, now.Unix()},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeTimestamp}, now.Unix(), now.Unix()},
		{config.MetaFieldConfig{Type: define.MetaFieldTypeTimestamp}, conv.String(now.Unix()), now.Unix()},
		{config.MetaFieldConfig{
			Type: define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				"time_format": "timestamp",
			},
		}, now.Unix(), now.Unix()},
		{config.MetaFieldConfig{
			Type: define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				"time_format": "epoch_millisecond",
			},
		}, now.Unix() * 1000, now.Unix()},
		{config.MetaFieldConfig{
			Type: define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				"time_format": "epoch_minute",
			},
		}, now.Unix() / 60, now.Unix() / 60 * 60},
		{config.MetaFieldConfig{
			Type: define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				"time_format": "timestamp",
				"time_zone":   zone,
			},
		}, now.Unix(), now.Unix()},
		{config.MetaFieldConfig{
			Type: define.MetaFieldTypeTimestamp,
			Option: map[string]interface{}{
				"time_format": "datetime",
				"time_zone":   zone,
			},
		}, now.Format("2006-01-02 15:04:05"), now.Unix()},
	}
	for i, c := range cases {
		fn := etl.NewTransformByField(&c.field)
		result, err := fn(c.value)
		s.NoError(err, i)
		s.Equal(c.timestamp, result.(types.TimeStamp).Int64(), i)
	}
}

// TestNewTransformByFieldSuite :
func TestNewTransformByFieldSuite(t *testing.T) {
	suite.Run(t, new(TransformByFieldSuite))
}

// TransformBySeparatorSuite
type TransformBySeparatorSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *TransformBySeparatorSuite) TestUsage() {
	cases := []struct {
		input       string
		transformer etl.TransformFn
		result      map[string]interface{}
	}{
		{
			`1,2`,
			etl.TransformMapBySeparator(",", []string{"x", "y"}),
			map[string]interface{}{
				"x": "1",
				"y": "2",
			},
		},
		{
			`1`,
			etl.TransformMapBySeparator(",", []string{"x", "y"}),
			map[string]interface{}{
				"x": "1",
				"y": nil,
			},
		},
		{
			``,
			etl.TransformMapBySeparator(",", []string{"x", "y"}),
			map[string]interface{}{
				"x": nil,
				"y": nil,
			},
		},
		{
			`1,2`,
			etl.TransformMapByRegexp(`(?P<x>\w+)[,\s]*(?P<y>\w+)`),
			map[string]interface{}{
				"x": "1",
				"y": "2",
			},
		},
		{
			`1`,
			etl.TransformMapByRegexp(`(?P<x>\w+)[,\s]*(?P<y>\w+)`),
			map[string]interface{}{
				"x": nil,
				"y": nil,
			},
		},
		{
			`1`,
			etl.TransformMapByRegexp(`(?P<x>\w+)[,\s]*(?P<y>\w+)?`),
			map[string]interface{}{
				"x": "1",
				"y": "",
			},
		},
		{
			``,
			etl.TransformMapByRegexp(`(?P<x>\w+)[,\s]*(?P<y>\w+)`),
			map[string]interface{}{
				"x": nil,
				"y": nil,
			},
		},
		{
			`{"x": "1", "y": 2}`,
			etl.TransformMapByJSON,
			map[string]interface{}{
				"x": "1",
				"y": 2.0,
			},
		},
	}

	for i, c := range cases {
		value, err := c.transformer(c.input)
		s.NoError(err, i)
		result, ok := value.(map[string]interface{})
		s.True(ok)
		s.MapEqual(c.result, result)
	}
}

// TestTransformBySeparatorSuite
func TestTransformBySeparatorSuite(t *testing.T) {
	suite.Run(t, new(TransformBySeparatorSuite))
}
