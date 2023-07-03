// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

var encodings = []string{
	"GBK",
	"GB18030",
	"HZ-GB2312",
	"Big5",
	"UTF-8",
}

func TestEncodingMaps(t *testing.T) {
	for _, name := range encodings {
		_, ok := utils.EncodingMaps[name]
		if !ok {
			t.Errorf("unknown encoding %s", name)
		}
	}
}

func TestNewEncoderDecoder(t *testing.T) {
	encodings := map[string]string{
		"GBK":       "测试",
		"GB18030":   "测试",
		"HZ-GB2312": "测试",
		"Big5":      "雙雙",
		"UTF-8":     "测试",
	}

	for name, value := range encodings {
		encoder := utils.NewEncoder(name)
		encoded, err := encoder.String(value)
		if err != nil {
			t.Errorf("encode by %v error: %v", name, err)
		}

		decoder := utils.NewDecoder(name)
		decoded, err := decoder.String(encoded)
		if err != nil {
			t.Errorf("decode by %v error: %v", name, err)
		}
		if value != decoded {
			t.Errorf("convert from %v to %v, except %v", encoded, decoded, value)
		}
	}
}
