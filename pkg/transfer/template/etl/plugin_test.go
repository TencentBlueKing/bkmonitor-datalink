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
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SchemaByResultTablePluginSuite
type SchemaByResultTablePluginSuite struct {
	testsuite.ETLSuite
}

// MakeFieldConfig
func (s *SchemaByResultTablePluginSuite) MakeFieldConfig(name, alias string, fType define.MetaFieldType, tag define.MetaFieldTagType, option map[string]interface{}) *config.MetaFieldConfig {
	return &config.MetaFieldConfig{
		IsConfigByUser: true,
		FieldName:      name,
		AliasName:      alias,
		Type:           fType,
		Tag:            tag,
		Option:         option,
	}
}

// TestUsage
func (s *SchemaByResultTablePluginSuite) TestUsage() {
	now := time.Now()
	cases := []struct {
		fields        []*config.MetaFieldConfig
		input, result map[string]interface{}
	}{
		{
			[]*config.MetaFieldConfig{
				s.MakeFieldConfig("timestamp", "time", define.MetaFieldTypeTimestamp, define.MetaFieldTagTime, nil),
			},
			map[string]interface{}{
				"timestamp": now,
			},
			map[string]interface{}{
				"time": now.Unix(),
			},
		},
		{
			[]*config.MetaFieldConfig{
				s.MakeFieldConfig("timestamp", "time", define.MetaFieldTypeTimestamp, define.MetaFieldTagTime, nil),
				s.MakeFieldConfig("key", "", define.MetaFieldTypeString, define.MetaFieldTagDimension, nil),
				s.MakeFieldConfig("value", "", define.MetaFieldTypeInt, define.MetaFieldTagMetric, nil),
			},
			map[string]interface{}{
				"timestamp": now,
				"key":       "x",
				"value":     1,
			},
			map[string]interface{}{
				"time": now.Unix(),
				"dimensions": map[string]interface{}{
					"key": "x",
				},
				"metrics": map[string]interface{}{
					"value": 1,
				},
			},
		},
		{
			[]*config.MetaFieldConfig{
				s.MakeFieldConfig("timestamp", "time", define.MetaFieldTypeTimestamp, define.MetaFieldTagTime, nil),
				s.MakeFieldConfig("key", "", define.MetaFieldTypeString, define.MetaFieldTagDimension, map[string]interface{}{
					config.MetaFieldOptRealPath: "dimensions.key",
				}),
				s.MakeFieldConfig("value", "", define.MetaFieldTypeInt, define.MetaFieldTagMetric, map[string]interface{}{
					config.MetaFieldOptRealPath: "metrics.value",
				}),
			},
			map[string]interface{}{
				"timestamp": now,
				"dimensions": map[string]interface{}{
					"key": "x",
				},
				"metrics": map[string]interface{}{
					"value": 1,
				},
			},
			map[string]interface{}{
				"time": now.Unix(),
				"dimensions": map[string]interface{}{
					"key": "x",
				},
				"metrics": map[string]interface{}{
					"value": 1,
				},
			},
		},
		{
			[]*config.MetaFieldConfig{
				s.MakeFieldConfig("time", "time", define.MetaFieldTypeTimestamp, define.MetaFieldTagTime, nil),
				s.MakeFieldConfig("time", "x1", define.MetaFieldTypeTimestamp, define.MetaFieldTagDimension, nil),
				s.MakeFieldConfig("time", "x2", define.MetaFieldTypeTimestamp, define.MetaFieldTagDimension, nil),
				s.MakeFieldConfig("time", "x3", define.MetaFieldTypeTimestamp, define.MetaFieldTagMetric, nil),
				s.MakeFieldConfig("time", "x4", define.MetaFieldTypeTimestamp, define.MetaFieldTagDimension, nil),
			},
			map[string]interface{}{
				"time": now,
			},
			map[string]interface{}{
				"time": now.Unix(),
				"dimensions": map[string]interface{}{
					"x1": now.Unix(),
					"x2": now.Unix(),
					"x4": now.Unix(),
				},
				"metrics": map[string]interface{}{
					"x3": now.Unix(),
				},
			},
		},
		{
			[]*config.MetaFieldConfig{
				s.MakeFieldConfig("value1", "value", define.MetaFieldTypeInt, define.MetaFieldTagMetric, nil),
				s.MakeFieldConfig("value2", "value", define.MetaFieldTypeInt, define.MetaFieldTagMetric, nil),
			},
			map[string]interface{}{
				"value1": "1",
				"value2": "2",
			},
			map[string]interface{}{
				"metrics": map[string]interface{}{
					"value": 2,
				},
			},
		},
		{
			[]*config.MetaFieldConfig{
				s.MakeFieldConfig("value2", "value", define.MetaFieldTypeInt, define.MetaFieldTagMetric, nil),
				s.MakeFieldConfig("value1", "value", define.MetaFieldTypeInt, define.MetaFieldTagMetric, nil),
			},
			map[string]interface{}{
				"value1": "1",
				"value2": "2",
			},
			map[string]interface{}{
				"metrics": map[string]interface{}{
					"value": 1,
				},
			},
		},
	}

	for i, c := range cases {
		builder := etl.NewContainerSchemaBuilder()
		s.NoError(builder.Apply(template.SchemaByResultTablePlugin(&config.MetaResultTableConfig{
			FieldList: c.fields,
		})), i)
		schema := builder.Finish()
		from := etl.NewMapContainerFrom(c.input)
		to := etl.NewMapContainer()
		s.NoError(schema.Transform(from, to), i)
		s.JSONEqual(c.result, to)
	}
}

// TestSchemaByResultTablePluginSuite
func TestSchemaByResultTablePluginSuite(t *testing.T) {
	suite.Run(t, new(SchemaByResultTablePluginSuite))
}

// PrepareByResultTablePluginSuite
type PrepareByResultTablePluginSuite struct {
	testsuite.ETLSuite
}

// TestDefaultGetSeparatorFieldByOption
func (s *PrepareByResultTablePluginSuite) TestDefaultGetSeparatorFieldByOption() {
	var cases []*config.MetaResultTableConfig
	cases = append(cases, &config.MetaResultTableConfig{})
	cases = append(cases, &config.MetaResultTableConfig{
		Option: map[string]interface{}{
			config.ResultTableOptSeparatorAction: "test",
		},
	})

	for i, c := range cases {
		f, err := template.GetSeparatorFieldByOption(nil, c)
		s.Nil(f, i)
		s.NoError(err, i)
	}
}

// TestPrepareByResultTablePlugin
func (s *PrepareByResultTablePluginSuite) TestPrepareByResultTablePlugin() {
	data := `{"x":1}`
	var cases []*config.MetaResultTableConfig
	cases = append(cases, &config.MetaResultTableConfig{
		Option: map[string]interface{}{
			config.ResultTableOptSeparatorAction:    "regexp",
			config.ResultTableOptLogSeparatorRegexp: `{"(?P<k>\w+)":(?P<v>\w+)}`,
		},
	})
	cases = append(cases, &config.MetaResultTableConfig{
		Option: map[string]interface{}{
			config.ResultTableOptSeparatorAction: "json",
		},
	})
	cases = append(cases, &config.MetaResultTableConfig{
		Option: map[string]interface{}{
			config.ResultTableOptSeparatorAction:       "delimiter",
			config.PipelineConfigOptLogSeparatedFields: []interface{}{"data"},
			config.PipelineConfigOptLogSeparator:       ",",
		},
	})

	for i, c := range cases {
		source := "data"
		target := "prepare"
		c.Option[config.ResultTableOptSeparatorNodeSource] = source
		c.Option[config.ResultTableOptSeparatorNode] = target

		f, err := template.GetSeparatorFieldByOption(nil, c)
		s.NotNil(f, i)
		s.NoError(err, i)

		from := etl.NewMapContainerFrom(map[string]interface{}{
			source: data,
		})
		to := etl.NewMapContainer()
		s.NoError(f.Transform(from, to))

		_, err = to.Get(target)
		s.NoError(err)
	}
}

// TestPrepareByResultTablePluginSuite
func TestPrepareByResultTablePluginSuite(t *testing.T) {
	suite.Run(t, new(PrepareByResultTablePluginSuite))
}
