// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
)

func TestPingServer(t *testing.T) {
	content := `
pingserver:
  disabled: false
  auto_reload: true
  patterns:
    - "../example/fixtures/pingserver_sub*.yml"
`
	conf := confengine.MustLoadConfigContent(content)
	ps, err := New(conf)
	assert.NoError(t, err)

	assert.Len(t, ps.config.Sub.Addrs(), 2)

	reloadContent := `
pingserver:
  disabled: false
  auto_reload: true
  patterns:
    - "../example/fixtures/pingserver_reload*.bak"
`
	assert.NoError(t, ps.Start())

	err = ps.Reload(confengine.MustLoadConfigContent(reloadContent))
	assert.NoError(t, err)
	assert.Len(t, ps.config.Sub.Addrs(), 1)
	ps.Stop()
}
