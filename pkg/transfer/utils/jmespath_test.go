// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type JMESPathSuite struct {
	suite.Suite
}

func (s *JMESPathSuite) TestSplit() {
	compiled, err := utils.CompileJMESPathCustom("{ip: split(target, '|')|[0], bk_cloud_id: split(target, '|')|[1]}")
	s.NoError(err)
	data := map[string]interface{}{"target": "127.0.0.1|2"}
	actual, err := compiled.Search(data)
	s.NoError(err)
	s.Equal(map[string]interface{}{"ip": "127.0.0.1", "bk_cloud_id": "2"}, actual)
}

func (s *JMESPathSuite) TestRegexExtract() {
	compiled, err := utils.CompileJMESPathCustom(`regex_extract(content, '(\w+).(\w+)') | {db: [1], table: [2]}`)
	s.NoError(err)
	data := map[string]interface{}{"content": "system.cpu_usage"}
	actual, err := compiled.Search(data)
	s.NoError(err)
	s.Equal(map[string]interface{}{"db": "system", "table": "cpu_usage"}, actual)
}

func (s *JMESPathSuite) TestGetField() {
	compiled, err := utils.CompileJMESPathCustom("{category: get_field({sys: 'system', db: 'database', cpu: 'CPU'}, cate)}")
	s.NoError(err)
	data := map[string]interface{}{"cate": "db"}
	actual, err := compiled.Search(data)
	s.NoError(err)
	s.Equal(map[string]interface{}{"category": "database"}, actual)
}

func (s *JMESPathSuite) TestToJSON() {
	compiled, err := utils.CompileJMESPathCustom("to_json('{\"name\": \"test\"}')")
	s.NoError(err)
	data := map[string]interface{}{}
	actual, err := compiled.Search(data)
	s.NoError(err)
	s.Equal(map[string]interface{}{"name": "test"}, actual)
}

func (s *JMESPathSuite) TestZip() {
	compiled, err := utils.CompileJMESPathCustom("zip(['a', 'b'], ['1', '2'])")
	s.NoError(err)
	data := map[string]interface{}{}
	actual, err := compiled.Search(data)
	s.NoError(err)
	s.Equal(map[string]interface{}{"a": "1", "b": "2"}, actual)
}

// JMESPathSuite :
func TestJMESPathSuite(t *testing.T) {
	suite.Run(t, new(JMESPathSuite))
}

func BenchmarkFieldMerge(b *testing.B) {
	var data interface{}
	jsonData := `{"event": {"tag": {"my": "test"}, "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`
	_ = json.Unmarshal([]byte(jsonData), &data)

	compiled, _ := utils.CompileJMESPathCustom("merge(event.tag, event.dimensions[?field=='device_name'].{device: value} | [0])")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compiled.Search(data)
	}
}

func BenchmarkRegexExtract(b *testing.B) {
	var data interface{}
	jsonData := `{"event": "127.0.0.1 CPU usage alert"}`
	_ = json.Unmarshal([]byte(jsonData), &data)

	compiled, _ := utils.CompileJMESPathCustom("regex_extract(event, '(\\d+)\\.(\\d+)\\.(\\d+)\\.(\\d+)') | [1]")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compiled.Search(data)
	}
}
