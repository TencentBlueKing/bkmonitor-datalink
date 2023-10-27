// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auto_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/auto"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SchemaByResultTablePluginSuite
type GetRecordNameByTagSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *GetRecordNameByTagSuite) TestUsage() {
	cases := []struct {
		input  define.MetaFieldTagType
		output string
	}{
		{define.MetaFieldTagTime, ""},
		{define.MetaFieldTagDimension, define.RecordDimensionsFieldName},
		{define.MetaFieldTagMetric, define.RecordMetricsFieldName},
		{define.MetaFieldTagGroup, define.RecordDimensionsFieldName},
	}

	for i, c := range cases {
		s.Equal(c.output, auto.GetRecordRootByTag(c.input), i)
	}
}

// TestGetRecordNameByTagSuite
func TestGetRecordNameByTagSuite(t *testing.T) {
	suite.Run(t, new(GetRecordNameByTagSuite))
}
