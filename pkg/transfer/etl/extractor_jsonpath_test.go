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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// ExtractByJSONPathSuite :
type ExtractByJSONPathSuite struct {
	suite.Suite
}

// TestUsage :
func (s *ExtractByJSONPathSuite) TestUsage() {
	var data map[string]interface{}
	s.NoError(json.Unmarshal([]byte(`{"store":{"book":[{"category":"reference","author":"Nigel Rees","title":"Sayings of the Century","price":8.95},{"category":"fiction","author":"Evelyn Waugh","title":"Sword of Honour","price":12.99},{"category":"fiction","author":"Herman Melville","title":"Moby Dick","isbn":"0-553-21311-3","price":8.99},{"category":"fiction","author":"J. R. R. Tolkien","title":"The Lord of the Rings","isbn":"0-395-19395-8","price":22.99}],"bicycle":{"color":"red","price":19.95}},"expensive":10}`), &data))
	container := etl.MapContainer(data)

	cases := []struct {
		path   string
		result interface{}
	}{
		{"$.expensive", 10.0},
		{"$.store.book[0].price", 8.95},
		{"$.store.book[-1].isbn", "0-395-19395-8"},
		{"$.store.book[0,1].price", []interface{}{8.95, 12.99}},
		{"$.store.book[0:2].price", []interface{}{8.95, 12.99, 8.99}},
		{"$.store.book[?(@.isbn)].price", []interface{}{8.99, 22.99}},
		{"$.store.book[?(@.price > 10)].title", []interface{}{"Sword of Honour", "The Lord of the Rings"}},
		{"$.store.book[:].price", []interface{}{8.95, 12.99, 8.99, 22.99}},
		{"$.store.book[?(@.author =~ /(?i).*REES/)].author", []interface{}{"Nigel Rees"}},
	}

	for _, c := range cases {
		extractor := etl.ExtractByJSONPath(c.path)
		value, err := extractor(container)

		s.Equal(c.result, value, c.path)
		s.NoError(err)
	}
}

// TestUsage :
func (s *ExtractByJSONPathSuite) TestCompileError() {
	var data map[string]interface{}
	extractor := etl.ExtractByJSONPath("x")
	container := etl.MapContainer(data)
	result, err := extractor(container)
	s.Nil(result)
	s.Error(err, "should start with '$'")
}

// TestExtractByJSONPath :
func TestExtractByJSONPath(t *testing.T) {
	suite.Run(t, new(ExtractByJSONPathSuite))
}
