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

// JSONPathSuite :
type JSONPathSuite struct {
	suite.Suite
}

// TestForEach :
func (s *JSONPathSuite) TestForEach() {
	var data map[string]interface{}
	s.NoError(json.Unmarshal([]byte(`{"store":{"book":[{"category":"reference","author":"Nigel Rees","title":"Sayings of the Century","price":8.95},{"category":"fiction","author":"Evelyn Waugh","title":"Sword of Honour","price":12.99},{"category":"fiction","author":"Herman Melville","title":"Moby Dick","isbn":"0-553-21311-3","price":8.99},{"category":"fiction","author":"J. R. R. Tolkien","title":"The Lord of the Rings","isbn":"0-395-19395-8","price":22.99}],"bicycle":{"color":"red","price":19.95}},"expensive":10}`), &data))
	jpath, err := utils.CompileJSONPath(`$.store.book[0,1].price`)
	s.NoError(err)
	result := []interface{}{8.95, 12.99}
	s.NoError(jpath.ForEach(data, func(index int, value interface{}) {
		s.Equal(result[index], value)
	}))
}

// TestJSONPathSuite :
func TestJSONPathSuite(t *testing.T) {
	suite.Run(t, new(JSONPathSuite))
}
