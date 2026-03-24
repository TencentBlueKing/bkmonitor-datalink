// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testkits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func MustProcess(t *testing.T, f processor.Processor, r define.Record) {
	_, err := f.Process(&r)
	assert.NoError(t, err)
}

func AssertAttrsFound(t *testing.T, attrs pcommon.Map, key string) {
	assertAttrsKey(t, attrs, key, true)
}

func AssertAttrsNotFound(t *testing.T, attrs pcommon.Map, key string) {
	assertAttrsKey(t, attrs, key, false)
}

func assertAttrsKey(t *testing.T, attrs pcommon.Map, key string, found bool) {
	_, ok := attrs.Get(key)
	assert.Equal(t, ok, found)
}

func AssertAttrsStringKeyVal(t *testing.T, attrs pcommon.Map, kv ...string) {
	if len(kv)%2 != 0 {
		panic("kv must be even")
	}

	for i := 0; i < len(kv); i += 2 {
		key, val := kv[i], kv[i+1]
		v, ok := attrs.Get(key)
		if ok {
			assert.Equal(t, val, v.StringVal())
		} else {
			assert.Equal(t, val, "")
		}
	}
}

func AssertAttrsIntVal(t *testing.T, attrs pcommon.Map, key string, val int) {
	v, ok := attrs.Get(key)
	assert.True(t, ok)
	assert.Equal(t, int64(val), v.IntVal())
}
