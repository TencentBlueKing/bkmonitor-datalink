// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"strings"

	"github.com/cstockton/go-conv"
)

// Record :
type Record struct {
	Time       int64                  `json:"time"`
	Dimensions map[string]interface{} `json:"dimensions"`
	Metrics    map[string]interface{} `json:"metrics"`
	Exemplar   map[string]interface{} `json:"exemplar"`
}

func (r *Record) GetDimensions() map[string]string {
	dim := make(map[string]string)
	for k, v := range r.Dimensions {
		// 判断是否为数值类型
		if isNumeric(v) {
			dim[k] = makeNumericString(conv.String(v))
			continue
		}
		// 判断是否为布尔类型
		if _, ok := v.(bool); ok {
			dim[k] = conv.String(v)
			continue
		}
		// 判断是否为字符串类型
		if s, ok := v.(string); ok {
			dim[k] = s
			continue
		}
		// 其他情况赋空值
		dim[k] = ""
	}
	return dim
}

func isNumeric(i interface{}) bool {
	switch i.(type) {
	case int8, uint8, int16, uint16, int32, uint32, int64, uint64, int, uint:
		return true
	case float32, float64:
		return true
	default:
		return false
	}
}

func makeNumericString(from string) string {
	dot := strings.LastIndexFunc(from, func(r rune) bool {
		return r == '.'
	})
	if dot < 0 {
		return from
	}

	pos := len(from) - 1
loop:
	for ; pos > dot; pos-- {
		switch from[pos] {
		case '0', '.':
			continue
		default:
			pos++
			break loop
		}
	}

	return from[:pos]
}

// Clean
func (r *Record) Clean() bool {
	for key, value := range r.Metrics {
		if value == nil {
			delete(r.Metrics, key)
		}
	}

	return len(r.Metrics) > 0
}
