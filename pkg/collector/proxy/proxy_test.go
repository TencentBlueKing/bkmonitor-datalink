// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestProxy(t *testing.T) {
	content := `
proxy:
  disabled: true
  http:
    middlewares:
      logging
`
	config, err := confengine.LoadConfigContent(content)
	assert.NoError(t, err)

	proxy, err := New(config)
	assert.NoError(t, err)
	assert.NoError(t, proxy.Start())
	assert.NoError(t, proxy.Stop())
}

func TestValidatePreCheckProcessors(t *testing.T) {
	t.Run("nil pipeline getter", func(t *testing.T) {
		code, p, err := validatePreCheckProcessors(nil, nil)
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Equal(t, "", p)
		assert.NoError(t, err)
	})

	t.Run("noop pipeline getter", func(t *testing.T) {
		r := &define.Record{RecordType: define.RecordTraces}
		code, p, err := validatePreCheckProcessors(r, testkits.NewNoopPipeline())
		assert.Equal(t, define.StatusBadRequest, code)
		assert.Equal(t, "", p)
		assert.Error(t, err)
	})
}
