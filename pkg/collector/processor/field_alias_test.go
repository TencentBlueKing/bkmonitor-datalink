// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
)

func TestFieldAlias(t *testing.T) {
	conf, err := confengine.LoadConfigPath("../example/fixtures/platform.yml")
	assert.NoError(t, err)

	err = LoadAlias(conf)
	assert.NoError(t, err)

	alias1 := AttributeAlias.Get("http.method")
	assert.Equal(t, []string{"http.method", "http.request.method"}, alias1)

	alias2 := AttributeAlias.Get("test")
	assert.Equal(t, []string{"test"}, alias2)

	alias3 := ResourceAlias.Get("test")
	assert.Equal(t, []string{"test"}, alias3)

	alias4 := ResourceAlias.Get("telemetry.sdk.language")
	assert.Equal(t, []string{"telemetry.sdk.language", "telemetry.sdk.lang"}, alias4)
}
