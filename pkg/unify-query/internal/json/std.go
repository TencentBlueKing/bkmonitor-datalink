// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build !jsonsonic

package json

import (
	"encoding/json"
	"io"
)

type Number struct {
	json.Number
}

// Encoder 接口定义（与 sonic.Encoder 兼容）
type Encoder interface {
	Encode(v any) error
	SetEscapeHTML(on bool)
}

// Decoder 接口定义（与 sonic.Decoder 兼容）
type Decoder interface {
	Decode(v any) error
	UseNumber()
}

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func NewEncoder(w io.Writer) Encoder {
	return &encoderWrapper{json.NewEncoder(w)}
}

func NewDecoder(r io.Reader) Decoder {
	return &decoderWrapper{json.NewDecoder(r)}
}

// encoderWrapper 包装标准库的 Encoder
type encoderWrapper struct {
	*json.Encoder
}

func (e *encoderWrapper) SetEscapeHTML(on bool) {
	e.Encoder.SetEscapeHTML(on)
}

// decoderWrapper 包装标准库的 Decoder
type decoderWrapper struct {
	*json.Decoder
}

func (d *decoderWrapper) UseNumber() {
	d.Decoder.UseNumber()
}
