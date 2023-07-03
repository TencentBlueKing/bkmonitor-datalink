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

	"github.com/stretchr/testify/suite"

	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/conv"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// StringConvSuite
type StringConvSuite struct {
	suite.Suite
}

// TestFloatToString
func (s *StringConvSuite) TestFloatToString() {
	cases := []struct {
		value  interface{}
		expect string
	}{
		{12000002.0, "12000002"},
		{12000002000002.0, "12000002000002"},
		{12000002000002.1, "12000002000002.1"},
		{12000002000002.5, "12000002000002.5"},
	}

	for i, c := range cases {
		val, err := etl.TransformNilString(c.value)
		s.NoError(err)
		s.Equalf(c.expect, val, "%v", i)
	}
}

// TestStringConvSuite
func TestStringConvSuite(t *testing.T) {
	suite.Run(t, new(StringConvSuite))
}
