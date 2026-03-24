// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build jsonsonic

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

func Marshal(v any) ([]byte, error) {
	return sonicAPI.Marshal(v)
}

func Unmarshal(data []byte, v any) error {
	return sonicAPI.Unmarshal(data, v)
}

func NewEncoder(w io.Writer) Encoder {
	return &sonicEncoderWrapper{sonicAPI.NewEncoder(w)}
}

func NewDecoder(r io.Reader) Decoder {
	return &sonicDecoderWrapper{sonicAPI.NewDecoder(r)}
}

// Encoder 接口定义
type Encoder interface {
	Encode(v any) error
	SetEscapeHTML(on bool)
}

// Decoder 接口定义
type Decoder interface {
	Decode(v any) error
	UseNumber()
}

// sonicEncoderWrapper 包装 sonic 的 Encoder
type sonicEncoderWrapper struct {
	sonic.Encoder
}

func (e *sonicEncoderWrapper) SetEscapeHTML(on bool) {
	// sonic 的 Encoder 不支持 SetEscapeHTML，使用配置控制
	// 这里保持空实现，因为 sonic 的配置在初始化时已设置
}

// sonicDecoderWrapper 包装 sonic 的 Decoder
type sonicDecoderWrapper struct {
	sonic.Decoder
}

func (d *sonicDecoderWrapper) UseNumber() {
	// sonic 的 Decoder 不支持 UseNumber，使用配置控制
	// 这里保持空实现，因为 sonic 默认使用 Number 类型
}
