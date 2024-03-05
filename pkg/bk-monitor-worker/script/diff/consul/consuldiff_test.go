// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"testing"

	"github.com/stretchr/testify/assert"
)


func TestComparePath(t *testing.T) {
	src := []string{"test/test1", "test/test2", "test/test3"}
	bypass := []string{"test/test1", "test/test4"}
	onlySrc, onlyBypass := comparePath(&src, &bypass)
	
	assert.Equal(t, len(onlySrc), 2)
	assert.Equal(t, onlySrc, []string{"test/test2", "test/test3"})
	assert.Equal(t, len(onlyBypass), 1)
	assert.Equal(t, onlyBypass, []string{"test/test4"})
}
