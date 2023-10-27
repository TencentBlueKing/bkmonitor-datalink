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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ContainerSchemaSuite
type ContainerSchemaBuilderSuite struct {
	testsuite.ETLSuite
	builder *etl.ContainerSchemaBuilder
}

// SetupTest
func (s *ContainerSchemaBuilderSuite) SetupTest() {
	s.ETLSuite.SetupTest()
	s.builder = etl.NewContainerSchemaBuilder()
}

// TestStandardRecordPlugin
func (s *ContainerSchemaBuilderSuite) TestStandardRecordPlugin() {
	cases := []struct {
		name   string
		plugin etl.ContainerSchemaBuilderPlugin
	}{
		{
			define.RecordDimensionsFieldName,
			etl.SchemaDimensionsPlugin(),
		},
		{
			define.RecordMetricsFieldName,
			etl.SchemaMetricsPlugin(),
		},
		{
			define.RecordDimensionsFieldName,
			etl.SchemaGroupPlugin(),
		},
	}

	for i, c := range cases {
		builder := etl.NewContainerSchemaBuilder()
		s.NoError(builder.Apply(c.plugin), i)
		s.Len(builder.Records, 1, i)
		s.Equal(c.name, builder.Records[0].Name(), i)
	}
}

// TestAsStandard
func (s *ContainerSchemaBuilderSuite) TestAsStandard() {
	s.NoError(s.builder.Apply(
		etl.StandardTimeFieldPlugin(etl.ExtractByJMESPath(`time`)),
		etl.SchemaDimensionsPlugin(
			etl.NewSimpleField(`key`, etl.ExtractByJMESPath(`dimensions.key`), etl.TransformString),
		),
		etl.SchemaMetricsPlugin(
			etl.NewSimpleField(`value`, etl.ExtractByJMESPath(`metrics.value`), etl.TransformFloat64),
		),
	))

	schema := s.builder.Finish()

	now := time.Now()
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"time":       now.Unix(),
		"dimensions": map[string]interface{}{"key": "x"},
		"metrics":    map[string]interface{}{"value": 1},
	})
	to := etl.NewMapContainer()

	s.NoError(schema.Transform(from, to))

	s.JSONEqual(from, to)
}

// TestAsFree
func (s *ContainerSchemaBuilderSuite) TestAsCustom() {
	s.NoError(s.builder.Apply(
		etl.SchemaSimpleFieldPlugin(`labels`, etl.ExtractByJMESPath(`[group]`), etl.TransformAsIs),
		etl.SchemaSimpleFieldPlugin(`others`, etl.ExtractByJMESPath(`locations[?state != 'NY']`), etl.TransformAsIs),
		etl.SchemaSimpleRecordPlugin(`location`,
			etl.NewSimpleField(
				`city`, etl.ExtractByJMESPath(`locations[?state == 'NY'].name | [0]`), etl.TransformAsIs,
			),
			etl.NewSimpleField(
				`state`, etl.ExtractByJMESPath(`locations[?state == 'NY'].state | [0]`), etl.TransformAsIs,
			),
		),
	))

	schema := s.builder.Finish()
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"group": "bravo",
		"locations": []map[string]interface{}{
			{"name": "Seattle", "state": "WA"},
			{"name": "New York", "state": "NY"},
			{"name": "Bellevue", "state": "WA"},
			{"name": "Olympia", "state": "WA"},
		},
	})
	to := etl.NewMapContainer()

	s.NoError(schema.Transform(from, to))
	s.JSONEqual(map[string]interface{}{
		"labels": []string{"bravo"},
		"location": map[string]interface{}{
			"city": "New York", "state": "NY",
		},
		"others": []map[string]interface{}{
			{"name": "Seattle", "state": "WA"},
			{"name": "Bellevue", "state": "WA"},
			{"name": "Olympia", "state": "WA"},
		},
	}, to)
}

// TestPreparePlugin
func (s *ContainerSchemaBuilderSuite) TestPreparePlugin() {
	s.NoError(s.builder.Apply(etl.SchemaPreparePlugin(etl.NewSimpleRecord([]etl.Field{
		etl.NewSimpleField(`location`, etl.ExtractByJMESPath(`locations[?state == 'NY'] | [0]`), etl.TransformAsIs),
	}))))

	schema := s.builder.Finish()
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"locations": []map[string]interface{}{
			{"name": "Seattle", "state": "WA"},
			{"name": "New York", "state": "NY"},
			{"name": "Bellevue", "state": "WA"},
			{"name": "Olympia", "state": "WA"},
		},
	})

	s.NoError(schema.Transform(from, nil))

	s.JSONEqual(map[string]interface{}{
		"location": map[string]interface{}{
			"name": "New York", "state": "NY",
		},
		"locations": []map[string]interface{}{
			{"name": "Seattle", "state": "WA"},
			{"name": "New York", "state": "NY"},
			{"name": "Bellevue", "state": "WA"},
			{"name": "Olympia", "state": "WA"},
		},
	}, from)
}

// TestReprocessPlugin
func (s *ContainerSchemaBuilderSuite) TestReprocessPlugin() {
	s.NoError(s.builder.Apply(
		etl.SchemaSimpleFieldPlugin(`data`, etl.ExtractByJMESPath(`locations`), etl.TransformAsIs),
		etl.SchemaReprocessPlugin(etl.NewSimpleRecord([]etl.Field{
			etl.NewSimpleField(`location`, etl.ExtractByJMESPath(`data[?state == 'NY'] | [0]`), etl.TransformAsIs),
		}))),
	)

	schema := s.builder.Finish()
	from := etl.NewMapContainerFrom(map[string]interface{}{
		"locations": []map[string]interface{}{
			{"name": "Seattle", "state": "WA"},
			{"name": "New York", "state": "NY"},
			{"name": "Bellevue", "state": "WA"},
			{"name": "Olympia", "state": "WA"},
		},
	})
	to := etl.NewMapContainer()

	s.NoError(schema.Transform(from, to))

	s.JSONEqual(map[string]interface{}{
		"location": map[string]interface{}{
			"name": "New York", "state": "NY",
		},
		"data": []map[string]interface{}{
			{"name": "Seattle", "state": "WA"},
			{"name": "New York", "state": "NY"},
			{"name": "Bellevue", "state": "WA"},
			{"name": "Olympia", "state": "WA"},
		},
	}, to)
}

// TestContainerSchemaBuilderSuite
func TestContainerSchemaBuilderSuite(t *testing.T) {
	suite.Run(t, new(ContainerSchemaBuilderSuite))
}
