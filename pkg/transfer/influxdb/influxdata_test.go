// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// RecordSuite
type RecordSuite struct {
	suite.Suite
}

// TestClean
func (s *RecordSuite) TestClean() {
	cases := []struct {
		item        influxdb.Record
		tags        map[string]string
		fieldLength int
		cleaned     bool
	}{
		{
			influxdb.Record{
				Dimensions: map[string]interface{}{
					"x":   "y",
					"a1":  "",
					"a2":  nil,
					"a3":  true,
					"a4":  1.14,
					"a5":  1024,
					"a6":  false,
					"a7":  1000000000000.10,
					"a8":  []int{1, 2},
					"a9":  []uint{},
					"a10": map[string]string{"k": "v"},
				},
				Metrics: map[string]interface{}{
					"usage": nil,
				},
			},
			map[string]string{
				"x":   "y",
				"a1":  "",
				"a2":  "",
				"a3":  "true",
				"a4":  "1.14",
				"a5":  "1024",
				"a6":  "false",
				"a7":  "1000000000000.1",
				"a8":  "",
				"a9":  "",
				"a10": "",
			},
			0,
			false,
		},
		{
			influxdb.Record{
				Dimensions: map[string]interface{}{
					"x": "y",
				},
				Metrics: map[string]interface{}{
					"nil": nil,
					"a":   "b",
				},
			},
			map[string]string{
				"x": "y",
			},
			1,
			true,
		},
		{
			influxdb.Record{
				Dimensions: map[string]interface{}{
					"x": "y",
				},
				Metrics: map[string]interface{}{
					"empty": "",
					"a":     "b",
				},
			},
			map[string]string{
				"x": "y",
			},
			2,
			true,
		},
	}

	for _, c := range cases {
		s.Equal(c.cleaned, c.item.Clean())
		s.Equal(c.tags, c.item.GetDimensions())
		s.Len(c.item.Metrics, c.fieldLength)
	}
}

// TestDecode
func (s *RecordSuite) TestDecode() {
	var record influxdb.Record
	s.NoError(json.Unmarshal(
		[]byte(`{"dimensions": {"int": 111111111, "string": "22222222", "float": 3333.4444}}`), &record,
	))
	s.Equal(3, len(record.Dimensions))
}

// TestDecodeBizNumber
func (s *RecordSuite) TestDecodeBizNumber() {
	var record influxdb.Record
	s.NoError(json.Unmarshal(
		[]byte(`{"dimensions": {"a": 123456789, "b": 123456789.0, "c": 123456789.1, "d": 1234567890}}`), &record,
	))
	s.Equal("123456789", record.Dimensions["a"])
	s.Equal("123456789", record.Dimensions["b"])
	s.Equal("123456789.1", record.Dimensions["c"])
	s.Equal("1234567890", record.Dimensions["d"])
}

// TestRecord
func TestRecord(t *testing.T) {
	suite.Run(t, new(RecordSuite))
}
