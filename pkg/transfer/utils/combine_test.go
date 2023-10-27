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
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// CartesianProductSuite
type CartesianProductSuite struct {
	suite.Suite
}

// TestProduct
func (s *CartesianProductSuite) TestProduct() {
	excepted := map[string]int{
		"+a1": 1,
		"+a2": 1,
		"+b1": 1,
		"+b2": 1,
		"+c1": 1,
		"+c2": 1,
	}

	product := utils.NewCombineHelper(map[string][]interface{}{
		"key":   {"a", "b", "c"},
		"index": {1, 2},
	})
	s.NoError(product.Product(map[string]interface{}{
		"flag": "+",
	}, func(result map[string]interface{}) error {
		key := fmt.Sprintf("%s%s%d", result["flag"], result["key"], result["index"])
		excepted[key] -= 1
		return nil
	}))

	for key, value := range excepted {
		s.Equal(0, value, key)
	}
}

// TestZip
func (s *CartesianProductSuite) TestZip() {
	excepted := map[string]int{
		"+a1": 1,
		"+b2": 1,
		"+c3": 1,
	}

	product := utils.NewCombineHelper(map[string][]interface{}{
		"key":   {"a", "b", "c"},
		"index": {1, 2, 3},
	})
	s.NoError(product.Zip(map[string]interface{}{
		"flag": "+",
	}, func(result map[string]interface{}) error {
		key := fmt.Sprintf("%s%s%d", result["flag"], result["key"], result["index"])
		excepted[key] -= 1
		return nil
	}))

	for key, value := range excepted {
		s.Equal(0, value, key)
	}
}

// TestNewCartesianProductSuite
func TestNewCartesianProductSuite(t *testing.T) {
	suite.Run(t, new(CartesianProductSuite))
}
