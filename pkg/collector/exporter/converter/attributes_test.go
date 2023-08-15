// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestMergeAttributeMaps(t *testing.T) {
	dst := pcommon.NewMap()
	dst.Upsert("key1", pcommon.NewValueInt(1))
	dst.Upsert("key2", pcommon.NewValueString("2"))

	// m1
	m1 := pcommon.NewMap()
	m1.Upsert("key1", pcommon.NewValueInt(10))
	m1.Upsert("key3", pcommon.NewValueBool(true))

	// m2
	m2 := pcommon.NewMap()
	m1.Upsert("key4", pcommon.NewValueBool(false))

	ret := MergeAttributeMaps(dst, m1, m2)
	assert.Len(t, ret, 4)
	assert.Equal(t, int64(10), ret["key1"])
	assert.Equal(t, "2", ret["key2"])
	assert.Equal(t, true, ret["key3"])
	assert.Equal(t, false, ret["key4"])
}
