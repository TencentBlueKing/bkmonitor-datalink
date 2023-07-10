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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestCommonProcessor(t *testing.T) {
	p := NewCommonProcessor(nil, nil)
	assert.Nil(t, p.MainConfig())
	assert.Nil(t, p.SubConfigs())
	p.Clean()
}

func TestRegisterCreateFunc(t *testing.T) {
	Register("NoopFuncForTest", func(config map[string]interface{}, customized []SubConfigProcessor) (Processor, error) {
		return nil, nil
	})

	fn := GetProcessorCreator("NoopFuncForTest/id")
	p, err := fn(nil, nil)
	assert.Nil(t, p)
	assert.Nil(t, err)

	inst := NewInstance("id1", p)
	assert.Equal(t, "id1", inst.ID())
}

func TestNonSchedRecords(t *testing.T) {
	PublishNonSchedRecords(&define.Record{})
	select {
	case <-NonSchedRecords():
	default:
	}
}
