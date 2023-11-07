// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonx

import (
	"github.com/bytedance/sonic"
)

var sonicAPI = sonic.Config{
	EscapeHTML:       true, // 安全性
	CompactMarshaler: true, // 兼容性
	CopyString:       true, // 正确性
}.Froze()

func Marshal(v interface{}) ([]byte, error) {
	return sonicAPI.Marshal(v)
}

func Unmarshal(data []byte, v interface{}) error {
	return sonicAPI.Unmarshal(data, v)
}

func MarshalString(v interface{}) (string, error) {
	return sonicAPI.MarshalToString(v)
}

func UnmarshalString(data string, v interface{}) error {
	return sonicAPI.UnmarshalFromString(data, v)
}
