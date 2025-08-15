// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package json

import (
	"encoding/json"
	"io"

	"github.com/bytedance/sonic"
)

type Number struct {
	json.Number
}

var sonicAPI = sonic.Config{
	EscapeHTML:       true, // 安全性
	CompactMarshaler: true, // 兼容性
	CopyString:       true, // 正确性
	SortMapKeys:      true, // 确保序列化结果稳定
}.Froze()

var stableSonicAPI = sonic.Config{
	EscapeHTML:       true, // 安全性
	CompactMarshaler: true, // 兼容性
	CopyString:       true, // 正确性
	SortMapKeys:      true, // 确保序列化结果稳定
}.Froze()

func StableMarshal(v interface{}) ([]byte, error) {
	return stableSonicAPI.Marshal(v)
}

func Marshal(v interface{}) ([]byte, error) {
	return sonicAPI.Marshal(v)
}

func Unmarshal(data []byte, v interface{}) error {
	return sonicAPI.Unmarshal(data, v)
}

func NewEncoder(w io.Writer) sonic.Encoder {
	return sonicAPI.NewEncoder(w)
}

func NewDecoder(r io.Reader) sonic.Decoder {
	return sonicAPI.NewDecoder(r)
}
