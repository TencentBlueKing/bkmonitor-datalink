// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package echo

import (
	"bytes"
	"context"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}

// TestBackend_Push : 正常提交
func TestBackendPush(t *testing.T) {
	cases := []string{
		`{"time":1558494970,"dimensions":{"tag":null},"metrics":{"field":1}}`,
	}

	backend, _ := NewEchoBackend(context.Background(), "test")
	backend.enable = true
	for _, value := range cases {
		payload := define.NewJSONPayloadFrom([]byte(value), 0)
		output := captureOutput(func() {
			backend.Push(payload, nil)
		})
		assert.NotEmpty(t, output)

		// 去掉时间前缀
		assert.Equal(t, "{\"time\":1558494970,\"dimensions\":{\"tag\":null},\"metrics\":{\"field\":1}}\n", output[20:])
	}
}
