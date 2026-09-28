// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeStaticReportFields(t *testing.T) {
	fields, err := NormalizeStaticReportFields([]string{"system.os", "net.interface.mac"})
	assert.NoError(t, err)
	assert.Contains(t, fields, StaticReportFieldSystemOS)
	assert.Contains(t, fields, StaticReportFieldNetInterfaceMac)
	assert.NotContains(t, fields, StaticReportFieldSystemPlatform)

	fields, err = NormalizeStaticReportFields([]string{"system"})
	assert.NoError(t, err)
	assert.Contains(t, fields, StaticReportFieldSystemHostname)
	assert.Contains(t, fields, StaticReportFieldSystemKernelVersion)

	fields, err = NormalizeStaticReportFields([]string{"*"})
	assert.NoError(t, err)
	assert.Nil(t, fields)

	fields, err = NormalizeStaticReportFields(nil)
	assert.NoError(t, err)
	assert.Nil(t, fields)

	_, err = NormalizeStaticReportFields([]string{"system.not_exists"})
	assert.Error(t, err)

	_, err = NormalizeStaticReportFields([]string{"*", "system.not_exists"})
	assert.Error(t, err)
}

func TestStaticTaskConfigCleanReportFields(t *testing.T) {
	cfg := NewStaticTaskConfig()
	cfg.ReportFields = []string{"system.os", "system.platVer"}
	assert.NoError(t, cfg.Clean())

	cfg.ReportFields = []string{"system.not_exists"}
	assert.Error(t, cfg.Clean())
}
