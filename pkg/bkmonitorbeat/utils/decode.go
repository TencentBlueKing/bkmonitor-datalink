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
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// EncodingMaps :
var EncodingMaps map[string]encoding.Encoding

// NewDecoder :
func NewDecoder(name string) *encoding.Decoder {
	ec, ok := EncodingMaps[name]
	if !ok {
		return nil
	}
	return ec.NewDecoder()
}

// NewEncoder :
func NewEncoder(name string) *encoding.Encoder {
	ec, ok := EncodingMaps[name]
	if !ok {
		return nil
	}
	return ec.NewEncoder()
}

func init() {
	EncodingMaps = make(map[string]encoding.Encoding)
	for _, list := range [][]encoding.Encoding{
		simplifiedchinese.All,
		traditionalchinese.All,
		unicode.All,
	} {
		for _, d := range list {
			val, ok := GetPtrByName(d, "Name")
			if !ok {
				continue
			}
			name, ok := val.(*string)
			if !ok {
				continue
			}
			EncodingMaps[*name] = d
		}
	}
}
