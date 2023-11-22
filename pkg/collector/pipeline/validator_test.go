// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

type noneValidator struct{}

func (noneValidator) GetProcessor(name string) processor.Instance {
	return nil
}

func (noneValidator) GetPipeline(rtype define.RecordType) Pipeline {
	return nil
}

func TestValidatePreCheckProcessors(t *testing.T) {
	t.Run("nil pipeline getter", func(t *testing.T) {
		code, p, err := validatePreCheckProcessors(nil, nil)
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Equal(t, "", p)
		assert.NoError(t, err)
	})

	t.Run("none pipeline getter", func(t *testing.T) {
		code, p, err := validatePreCheckProcessors(&define.Record{RequestType: "unknown"}, noneValidator{})
		assert.Equal(t, define.StatusBadRequest, code)
		assert.Equal(t, "", p)
		assert.Error(t, err)
	})

	t.Run("default", func(t *testing.T) {
		v := Validator{
			Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			},
		}
		code, p, err := v.Validate(&define.Record{RequestType: "unknown"})
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Equal(t, "", p)
		assert.NoError(t, err)
	})
}
