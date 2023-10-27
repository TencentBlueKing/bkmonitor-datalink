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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// ExtractByJMESPathSuite :
type ExtractByJMESPathSuite struct {
	suite.Suite
}

// TestUsage :
func (s *ExtractByJMESPathSuite) TestUsage() {
	cases := []struct {
		data   string
		path   string
		result interface{}
	}{
		{`{"a": "foo", "b": "bar", "c": "baz"}`, "a", "foo"},
		{`{"a": "1"}`, "*", []interface{}{"1"}},
		{`{"a": {"b": {"c": {"d": "value"}}}}`, "a.b.c.d", "value"},
		{`{"x": ["a", "b", "c", "d", "e", "f"]}`, "x[1]", "b"},
		{`{"a":{"b":{"c":[{"d":[0,[1,2]]},{"d":[3,4]}]}}}`, "a.b.c[0].d[1][0]", 1.0},
		{`{"x": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "x[0:5]", []interface{}{0.0, 1.0, 2.0, 3.0, 4.0}},
		{`{"x": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "x[:5]", []interface{}{0.0, 1.0, 2.0, 3.0, 4.0}},
		{`{"x": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "x[::2]", []interface{}{0.0, 2.0, 4.0, 6.0, 8.0}},
		{`{"x": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "x[::-1]", []interface{}{9.0, 8.0, 7.0, 6.0, 5.0, 4.0, 3.0, 2.0, 1.0, 0.0}},
		{`{"people":[{"first":"James","last":"d"},{"first":"Jacob","last":"e"},{"first":"Jayden","last":"f"},{"missing":"different"}],"foo":{"bar":"baz"}}`, "people[*].first", []interface{}{"James", "Jacob", "Jayden"}},
		{`{"tag": {"my": "test"}, "tag2": {"my2": "test2"}}`, "merge(tag, tag2)", map[string]interface{}{"my": "test", "my2": "test2"}},
	}

	for _, c := range cases {
		var data map[string]interface{}
		s.NoError(json.Unmarshal([]byte(c.data), &data))
		container := etl.MapContainer(data)
		extractor := etl.ExtractByJMESPath(c.path)
		value, err := extractor(container)

		s.Equal(c.result, value, c.path, c.data)
		s.NoError(err)
	}
}

// TestExtractByJMESPath :
func TestExtractByJMESPath(t *testing.T) {
	suite.Run(t, new(ExtractByJMESPathSuite))
}

func TestExtractByJMESPaths(t *testing.T) {
	cases := []struct {
		data   string
		paths  []string
		result interface{}
	}{
		{
			data:   `{"tag": {"my": "test", "wanted1": "option1"}, "tag2": {"my2": "test2", "wanted2": "option2"}}`,
			paths:  []string{"tag.wanted2", "tag2.wanted2"},
			result: "option2",
		},
		{
			data:   `{"tag": {"my": "test", "wanted1": "option1"}, "tag2": {"my2": "test2", "wanted2": "option2"}}`,
			paths:  []string{"tag.wanted2", "tag3.wanted2"},
			result: nil,
		},
		{
			data:   `{"tag": {"my": "test", "wanted1": "option1"}, "tag2": {"my2": "test2", "wanted1": "option2"}}`,
			paths:  []string{"tag.wanted1", "tag2.wanted1"},
			result: "option1", // 按顺序获取第一个
		},
	}

	for _, c := range cases {
		var data map[string]interface{}
		assert.NoError(t, json.Unmarshal([]byte(c.data), &data))
		container := etl.MapContainer(data)
		extractor := etl.ExtractByJMESMultiPath(c.paths...)
		value, err := extractor(container)
		assert.NoError(t, err)
		assert.Equal(t, c.result, value)
	}
}
