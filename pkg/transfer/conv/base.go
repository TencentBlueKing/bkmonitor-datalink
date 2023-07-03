// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package conv

import (
	"strconv"

	"github.com/cstockton/go-conv"
)

// Converter :
type Converter struct {
	conv.Conv
}

// String :
func (c Converter) String(from interface{}) (value string, err error) {
	switch v := from.(type) {
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return c.Conv.String(from)
	}
}

// DefaultConv :
var DefaultConv = Converter{}

func init() {
	conv.DefaultConv = DefaultConv
}
