// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"math"
	"strconv"
)

// numberOf is the float64 a value's bytes are kept as in a columnar run, and
// whether they can be: only when appendNumber writes that float64 back as the
// very same bytes. Whatever else a value is - a string, null, a number
// written with an exponent where the encoder writes none, a trailing zero, an
// integer past float64's precision - it is kept as its bytes, so a value
// reads back as given whichever way it was kept.
func numberOf(raw []byte) (float64, bool) {
	if len(raw) == 0 || len(raw) > 32 || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		return 0, false
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	var buffer [32]byte
	if string(appendNumber(buffer[:0], value)) != string(raw) {
		return 0, false
	}
	return value, true
}

// appendNumber writes a float64 as encoding/json writes one, which is how
// the numbers of an answer arrive: plain decimal, an exponent only below
// 1e-6 or from 1e21, its two-digit negative exponent shortened.
func appendNumber(dst []byte, value float64) []byte {
	format := byte('f')
	if abs := math.Abs(value); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, value, format, -1, 64)
	if format == 'e' {
		if n := len(dst); n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}
