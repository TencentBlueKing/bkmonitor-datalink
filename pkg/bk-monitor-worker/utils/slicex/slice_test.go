// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package slicex

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

// TestRemoveItem
func TestRemoveItem(t *testing.T) {
	srcSlice := []string{"a", "b", "c", "d"}
	expectedSlice := []string{"a", "c", "d"}

	dstSlice := RemoveItem(srcSlice, "b")
	assert.Equal(t, expectedSlice, dstSlice)
}

func TestRemoveDuplicate(t *testing.T) {
	float64List := []float64{1.1, 2.2, 3.3, 4.4, 2.2, 1.1}
	assert.ElementsMatch(t, []float64{1.1, 2.2, 3.3, 4.4}, RemoveDuplicate(&float64List))

	float32List := []float32{1.1, 2.2, 3.3, 4.4, 2.2, 1.1}
	assert.ElementsMatch(t, []float32{1.1, 2.2, 3.3, 4.4}, RemoveDuplicate(&float32List))

	int64List := []int64{1, 2, 3, 4, 2, 1}
	assert.ElementsMatch(t, []int64{1, 2, 3, 4}, RemoveDuplicate(&int64List))

	uintList := []uint{1, 2, 3, 4, 2, 1}
	assert.ElementsMatch(t, []uint{1, 2, 3, 4}, RemoveDuplicate(&uintList))

	stringList := []string{"1", "2", "3", "4", "1", "2"}
	assert.ElementsMatch(t, []string{"1", "2", "3", "4"}, RemoveDuplicate(&stringList))
}

func TestChunkSlice(t *testing.T) {
	for size := 0; size < 28; size++ {
		var bigSlice []int
		for i := 0; i < 25; i++ {
			bigSlice = append(bigSlice, i)
		}
		result := ChunkSlice(bigSlice, size)
		var realSize int
		if size > 0 {
			realSize = size
		} else {
			realSize = cfg.DefaultDBFilterSize
		}
		targetLen := int(math.Ceil(float64(len(bigSlice)) / float64(realSize)))
		assert.Equalf(t, targetLen, len(result), fmt.Sprintf("size:%v", realSize))
		var all []int
		for _, ls := range result {
			all = append(all, ls...)
		}
		assert.ElementsMatch(t, all, bigSlice)
	}
}
