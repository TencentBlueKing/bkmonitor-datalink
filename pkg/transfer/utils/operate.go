// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"fmt"
	"math"

	"github.com/pkg/errors"
)

// DivNumber :
func DivNumber(left, right interface{}) (float64, error) {
	leftNum, err := ParseNormalFloat64(left)
	if err != nil {
		return 0, errors.WithMessagef(err, "parse left number %v", left)
	}

	rightNum, err := ParseNormalFloat64(right)
	if err != nil {
		return 0, errors.WithMessagef(err, "parse right number %v", left)
	}

	if rightNum == 0 {
		return math.Inf(1), fmt.Errorf("right number is 0")
	}

	return leftNum / rightNum, nil
}

// IsStringInSlice : in expression
func IsStringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

// IsIntInSlice : in expression
func IsIntInSlice(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

// IsNotEmptyString : not empty string
func IsNotEmptyString(data interface{}) bool {
	if data == nil {
		return false
	}

	switch data.(type) {
	case string:
		if data.(string) == "" {
			return false
		}
		return true
	default:
		return false
	}
}
