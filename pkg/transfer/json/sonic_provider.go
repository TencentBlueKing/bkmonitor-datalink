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
	"bytes"
	"io"

	"github.com/bytedance/sonic"
)

type Provider struct {
	Payload interface{}
}

func (p Provider) ContentType() string {
	return "application/json"
}

func (p Provider) Body() (io.Reader, error) {
	buf := &bytes.Buffer{}
	err := NewEncoder(buf).Encode(p.Payload)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

var sonicAPI = sonic.Config{
	EscapeHTML:       true, // 安全需求
	CompactMarshaler: true, // 兼容需求
}.Froze()

func Marshal(v interface{}) ([]byte, error) {
	return sonicAPI.Marshal(v)
}

func Unmarshal(data []byte, v interface{}) error {
	return sonicAPI.Unmarshal(data, v)
}

func NewEncoder(writer io.Writer) Encoder {
	return sonicAPI.NewEncoder(writer)
}

var sonicFastAPI = sonic.Config{
	CompactMarshaler: true, // 兼容需求
}.Froze()

func MarshalFast(v interface{}) ([]byte, error) {
	return sonicFastAPI.Marshal(v)
}
