// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"runtime"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// BenchmarkMemoryPool 测试内存池技术的性能提升
func BenchmarkMemoryPool(b *testing.B) {
	b.ReportAllocs()

	nodeBuilder := NewNodeBuilder(nil)

	// 模拟大量节点创建
	for i := 0; i < b.N; i++ {
		info := cmdb.Matcher{
			"bcs_cluster_id": "cluster-1",
			"namespace":      "default",
			"pod":            "pod-" + string(rune(i)),
		}

		_, err := nodeBuilder.GetID("pod", info)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompression 测试压缩算法的内存节省效果
func BenchmarkCompression(b *testing.B) {
	b.ReportAllocs()

	nodeBuilder := NewNodeBuilder(nil)

	// 创建大量节点并测试压缩效果
	var nodeIDs []uint64
	for i := 0; i < b.N; i++ {
		info := cmdb.Matcher{
			"bcs_cluster_id": "cluster-" + string(rune(i%10)), // 10个不同的集群
			"namespace":      "default",
			"pod":            "pod-" + string(rune(i%1000)), // 1000个不同的pod
		}

		nodeID, err := nodeBuilder.GetID("pod", info)
		if err != nil {
			b.Fatal(err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}

	// 测试压缩信息的存储效率
	var compressedSize, originalSize int
	for _, nodeID := range nodeIDs {
		compressed := nodeBuilder.GetCompressedInfo(nodeID)
		original := nodeBuilder.GetOriginalInfoFromCompressed(compressed)

		compressedSize += len(compressed) * 4 // uint16键值对，每个4字节
		originalSize += len(original) * 32    // 估算字符串平均长度
	}

	b.Logf("压缩率: %.2f%%", float64(compressedSize)/float64(originalSize)*100)
}

// TestMemoryPoolEffectiveness 测试内存池的有效性
func TestMemoryPoolEffectiveness(t *testing.T) {
	// 记录初始内存状态
	var memStatsBefore, memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	nodeBuilder := NewNodeBuilder(nil)

	// 创建大量节点
	for i := 0; i < 10000; i++ {
		info := cmdb.Matcher{
			"bcs_cluster_id": "cluster-1",
			"namespace":      "default",
			"pod":            "pod-" + string(rune(i)),
		}

		_, err := nodeBuilder.GetID("pod", info)
		if err != nil {
			t.Fatal(err)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&memStatsAfter)

	// 计算内存分配差异
	mallocs := memStatsAfter.Mallocs - memStatsBefore.Mallocs
	frees := memStatsAfter.Frees - memStatsBefore.Frees

	t.Logf("内存分配次数: %d", mallocs)
	t.Logf("内存释放次数: %d", frees)
	t.Logf("内存泄漏: %d", mallocs-frees)
}

// TestCompressionRatio 测试压缩率
func TestCompressionRatio(t *testing.T) {
	nodeBuilder := NewNodeBuilder(nil)

	// 创建具有重复值的节点
	nodeCount := 1000
	var compressedTotal, originalTotal int

	for i := 0; i < nodeCount; i++ {
		info := cmdb.Matcher{
			"bcs_cluster_id": "cluster-" + string(rune(i%10)), // 10个不同的集群
			"namespace":      "default",
			"pod":            "pod-" + string(rune(i%100)), // 100个不同的pod名称
		}

		nodeID, err := nodeBuilder.GetID("pod", info)
		if err != nil {
			t.Fatal(err)
		}

		// 计算压缩前后的存储大小
		compressed := nodeBuilder.GetCompressedInfo(nodeID)
		original := nodeBuilder.GetOriginalInfoFromCompressed(compressed)

		compressedTotal += len(compressed) * 4 // 每个键值对4字节
		for k, v := range original {
			originalTotal += len(k) + len(v)
		}
	}

	compressionRatio := float64(compressedTotal) / float64(originalTotal) * 100
	t.Logf("原始数据大小: %d bytes", originalTotal)
	t.Logf("压缩后大小: %d bytes", compressedTotal)
	t.Logf("压缩率: %.2f%%", compressionRatio)

	if compressionRatio > 50 {
		t.Errorf("压缩率不理想，期望小于50%%，实际%.2f%%", compressionRatio)
	}
}

// BenchmarkStringDict 测试字符串字典的性能
func BenchmarkStringDict(b *testing.B) {
	dict := NewStringDict()

	b.ReportAllocs()

	// 测试字符串字典的压缩和解压性能
	for i := 0; i < b.N; i++ {
		str := "test-string-" + string(rune(i%1000))

		// 压缩
		id := dict.GetID(str)

		// 解压
		_ = dict.GetString(id)
	}
}
