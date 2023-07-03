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
	"encoding/hex"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const (
	ConvTypeRaw = "raw"
	ConvTypeHex = "hex"
)

// ConvertStringToBytes :
func ConvertStringToBytes(data string, typ string) ([]byte, error) {
	switch typ {
	case ConvTypeHex:
		return ConvertHexStringToBytes(data)
	case ConvTypeRaw:
		return []byte(data), nil
	}
	return []byte{}, define.ErrType
}

// ConvertHexStringToBytes :
func ConvertHexStringToBytes(data string) ([]byte, error) {
	return hex.DecodeString(data)
}
