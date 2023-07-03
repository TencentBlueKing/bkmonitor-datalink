// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"bytes"
	"encoding/gob"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// Converter :
type Converter interface {
	Unmarshal(data []byte, v interface{}) error
	Marshal(v interface{}) ([]byte, error)
}

// ModelConverter :
var ModelConverter Converter

// JSONConverter :
type JSONConverter struct{}

// Unmarshal :
func (JSONConverter) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// Marshal :
func (JSONConverter) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// GobConverter :
type GobConverter struct{}

// Unmarshal :
func (GobConverter) Unmarshal(data []byte, v interface{}) error {
	var (
		buf     = bytes.NewBuffer(data)
		decoder = gob.NewDecoder(buf)
	)

	return decoder.Decode(v)
}

// Marshal :
func (GobConverter) Marshal(v interface{}) ([]byte, error) {
	var (
		buf     = bytes.NewBuffer(nil)
		encoder = gob.NewEncoder(buf)
	)

	err := encoder.Encode(v)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func init() {
	ModelConverter = JSONConverter{}
}
