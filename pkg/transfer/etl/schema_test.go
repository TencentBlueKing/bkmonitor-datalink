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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ContainerSchemaSuite
type ContainerSchemaSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *ContainerSchemaSuite) TestUsage() {
	schema := etl.NewContainerSchema("test", func() etl.Container {
		return etl.NewMapContainer()
	}, []etl.Record{
		etl.NewSimpleRecord([]etl.Field{etl.NewSimpleField(
			`time`, etl.ExtractByJMESPath(`time`), etl.TransformTimeStamp,
		)}),
		etl.NewNamedSimpleRecord("dimensions", []etl.Field{etl.NewSimpleField(
			`key`, etl.ExtractByJMESPath(`dimensions.key`), etl.TransformString,
		)}),
		etl.NewNamedSimpleRecord("metrics", []etl.Field{etl.NewSimpleField(
			`value`, etl.ExtractByJMESPath(`metrics.value`), etl.TransformFloat64,
		)}),
	})

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

// TestMultiRecords
func (s *ContainerSchemaSuite) TestMultiRecords() {
	schema := etl.NewDefaultContainerSchema("test", []etl.Record{
		etl.NewSimpleRecord([]etl.Field{etl.NewConstantField("root1", "1")}),
		etl.NewSimpleRecord([]etl.Field{etl.NewConstantField("root2", "2")}),
		etl.NewNamedSimpleRecord("item", []etl.Field{etl.NewConstantField("item3", "3")}),
		etl.NewNamedSimpleRecord("item", []etl.Field{etl.NewConstantField("item4", "4")}),
		etl.NewNamedSimpleRecord("item.item5", []etl.Field{etl.NewConstantField("value", "5")}),
	})
	to := etl.NewMapContainer()
	s.NoError(schema.Transform(nil, to))
	s.JSONEqual(map[string]interface{}{
		"root1": "1",
		"root2": "2",
		"item": map[string]interface{}{
			"item3": "3",
			"item4": "4",
			"item5": map[string]interface{}{
				"value": "5",
			},
		},
	}, to)
}

// TestMultiRecordsOrdering
func (s *ContainerSchemaSuite) TestRecordsRewrite() {
	schema := etl.NewDefaultContainerSchema("test", []etl.Record{
		etl.NewSimpleRecord([]etl.Field{etl.NewConstantField("value", "1")}),
		etl.NewSimpleRecord([]etl.Field{etl.NewConstantField("value", "2")}),
	})
	to := etl.NewMapContainer()
	s.NoError(schema.Transform(nil, to))
	s.JSONEqual(map[string]interface{}{"value": "2"}, to)
}

// TestMultiRecordsOrdering
func (s *ContainerSchemaSuite) TestTypeConflict() {
	schema := etl.NewDefaultContainerSchema("test", []etl.Record{
		etl.NewSimpleRecord([]etl.Field{etl.NewConstantField("value", "1")}),
		etl.NewNamedSimpleRecord("value", []etl.Field{etl.NewConstantField("x", "2")}),
		etl.NewSimpleRecord([]etl.Field{etl.NewConstantField("never", "x")}),
	})
	to := etl.NewMapContainer()
	s.Error(schema.Transform(nil, to))
	s.JSONEqual(map[string]interface{}{"value": "1"}, to)
}

// TestContainerSchemaSuite
func TestContainerSchemaSuite(t *testing.T) {
	suite.Run(t, new(ContainerSchemaSuite))
}
