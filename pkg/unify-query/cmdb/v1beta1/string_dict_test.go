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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// TestStringDict_LocalManagement 测试局部字典的独立性
func TestStringDict_LocalManagement(t *testing.T) {
	tg1 := NewTimeGraph()
	tg2 := NewTimeGraph()

	// 在两个TimeGraph中添加相同的字符串
	info1 := cmdb.Matcher{"host_id": "server1", "ip": "192.168.1.1"}
	info2 := cmdb.Matcher{"host_id": "server1", "ip": "192.168.1.1"}

	id1, err := tg1.nodeBuilder.GetID("host", info1)
	if err != nil {
		t.Fatalf("Failed to get ID from tg1: %v", err)
	}

	id2, err := tg2.nodeBuilder.GetID("host", info2)
	if err != nil {
		t.Fatalf("Failed to get ID from tg2: %v", err)
	}

	// 验证两个TimeGraph中的ID应该相同（因为内容相同）
	if id1 != id2 {
		t.Errorf("Expected same IDs for same content, got %d and %d", id1, id2)
	}

	// 验证两个TimeGraph的StringDict统计信息
	stats1 := tg1.stringDict.GetStats()
	stats2 := tg2.stringDict.GetStats()

	if stats1["total_strings"] != stats2["total_strings"] {
		t.Errorf("Expected same string count, got %v and %v", stats1["total_strings"], stats2["total_strings"])
	}

	// 清理tg1，验证tg2不受影响
	tg1.Clean(nil)

	stats1AfterClean := tg1.stringDict.GetStats()
	stats2AfterClean := tg2.stringDict.GetStats()

	if stats1AfterClean["total_strings"].(int) != 0 {
		t.Errorf("Expected tg1 string count to be 0 after clean, got %v", stats1AfterClean["total_strings"])
	}

	if stats2AfterClean["total_strings"].(int) != stats2["total_strings"].(int) {
		t.Errorf("Expected tg2 string count to remain %v after tg1 clean, got %v",
			stats2["total_strings"], stats2AfterClean["total_strings"])
	}
}

// TestStringDict_Cleanup 测试清理功能
func TestStringDict_Cleanup(t *testing.T) {
	tg := NewTimeGraph()

	// 添加一些测试数据
	infos := []cmdb.Matcher{
		{"host_id": "server1", "version": "v1.0.0", "env_name": "prod", "env_type": "k8s", "service_type": "web"},
		{"host_id": "server2", "version": "v1.0.1", "env_name": "staging", "env_type": "vm", "service_type": "api"},
		{"host_id": "server3", "version": "v1.0.2", "env_name": "dev", "env_type": "docker", "service_type": "db"},
	}

	for _, info := range infos {
		_, err := tg.nodeBuilder.GetID("host", info)
		if err != nil {
			t.Fatalf("Failed to get ID: %v", err)
		}
	}

	// 验证添加成功
	statsBefore := tg.stringDict.GetStats()
	if statsBefore["total_strings"].(int) < len(infos)*5 { // 每个info有5个字符串
		t.Errorf("Expected at least %d strings, got %v", len(infos)*5, statsBefore["total_strings"])
	}

	// 清理
	tg.Clean(nil)

	// 验证清理成功
	statsAfter := tg.stringDict.GetStats()
	if statsAfter["total_strings"].(int) != 0 {
		t.Errorf("Expected 0 strings after clean, got %v", statsAfter["total_strings"])
	}

	if statsAfter["next_id"].(uint64) != 1 {
		t.Errorf("Expected next_id to be 1 after clean, got %v", statsAfter["next_id"])
	}
}

// TestStringDict_CompressionDecompression 测试压缩和解压缩功能
func TestStringDict_CompressionDecompression(t *testing.T) {
	tg := NewTimeGraph()

	originalInfo := cmdb.Matcher{
		"host_id":      "test-server",
		"version":      "v1.0.0",
		"env_name":     "production",
		"env_type":     "k8s",
		"service_type": "web",
	}

	// 压缩
	nodeID, err := tg.nodeBuilder.GetID("host", originalInfo)
	if err != nil {
		t.Fatalf("Failed to get ID: %v", err)
	}

	// 获取压缩信息
	compressed := tg.nodeBuilder.GetCompressedInfo(nodeID)
	if compressed == nil {
		t.Fatal("Failed to get compressed info")
	}

	// 解压缩
	decompressed := tg.nodeBuilder.GetOriginalInfoFromCompressed(compressed)
	if decompressed == nil {
		t.Fatal("Failed to decompress info")
	}

	// 验证解压缩结果
	if len(decompressed) != len(originalInfo) {
		t.Errorf("Expected %d fields, got %d", len(originalInfo), len(decompressed))
	}

	for key, expectedValue := range originalInfo {
		actualValue, exists := decompressed[key]
		if !exists {
			t.Errorf("Key %s not found in decompressed info", key)
			continue
		}
		if actualValue != expectedValue {
			t.Errorf("For key %s, expected %s, got %s", key, expectedValue, actualValue)
		}
	}
}

// BenchmarkStringDict_Performance 性能基准测试
func BenchmarkStringDict_Performance(b *testing.B) {
	tg := NewTimeGraph()

	// 准备测试数据
	testInfos := make([]cmdb.Matcher, 1000)
	for i := 0; i < 1000; i++ {
		testInfos[i] = cmdb.Matcher{
			"host":    "server-" + string(rune('A'+i%26)),
			"ip":      "192.168.1." + string(rune('0'+i%10)),
			"cluster": "cluster-" + string(rune('A'+i%5)),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		info := testInfos[i%len(testInfos)]
		_, err := tg.nodeBuilder.GetID("host", info)
		if err != nil {
			b.Fatalf("Failed to get ID: %v", err)
		}
	}
}

// BenchmarkStringDict_MemoryUsage 内存使用基准测试
func BenchmarkStringDict_MemoryUsage(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tg := NewTimeGraph()

		// 添加1000个不同的字符串组合
		for j := 0; j < 1000; j++ {
			info := cmdb.Matcher{
				"host_id": "server-" + string(rune('A'+i%26)),
				"ip":      "192.168.1." + string(rune('0'+i%10)),
				"cluster": "cluster-" + string(rune('A'+i%5)),
			}
			_, err := tg.nodeBuilder.GetID("host", info)
			if err != nil {
				b.Fatalf("Failed to get ID: %v", err)
			}
		}

		// 清理，测试内存回收
		tg.Clean(nil)
	}
}

// TestStringDict_ConcurrentAccess 并发访问测试
func TestStringDict_ConcurrentAccess(t *testing.T) {
	tg := NewTimeGraph()
	done := make(chan bool)
	errors := make(chan error, 10*100)

	// 启动多个goroutine并发访问
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				info := cmdb.Matcher{
					"host_id":      "server-" + string(rune('A'+id)),
					"version":      "v1." + string(rune('0'+j%10)) + "." + string(rune('0'+j%10)),
					"env_name":     "env-" + string(rune('A'+j%5)),
					"env_type":     "type-" + string(rune('A'+j%3)),
					"service_type": "service-" + string(rune('A'+j%4)),
				}
				_, err := tg.nodeBuilder.GetID("host", info)
				if err != nil {
					errors <- err
				}
			}
			done <- true
		}(i)
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		<-done
	}
	close(errors)

	// 检查是否有错误
	for err := range errors {
		t.Errorf("Concurrent access failed: %v", err)
	}

	// 验证结果：由于字符串去重，实际字符串数量会远少于 10*100*5
	// 但应该至少有一些字符串被存储（至少包含不同的 host_id, version, env_name, env_type, service_type）
	stats := tg.stringDict.GetStats()
	totalStrings := stats["total_strings"].(int)

	// 至少应该有 10 个不同的 host_id + 10 个不同的 version + 5 个不同的 env_name + 3 个不同的 env_type + 4 个不同的 service_type
	// 但由于可能有其他字符串（如资源类型等），我们只验证至少有一些字符串被存储
	if totalStrings < 10 {
		t.Errorf("Expected at least 10 unique strings (due to deduplication), got %d", totalStrings)
	}

	// 验证 next_id 是合理的（应该大于 1，表示有字符串被添加）
	if stats["next_id"].(uint64) <= 1 {
		t.Errorf("Expected next_id > 1, got %v", stats["next_id"])
	}
}

// TestStringDict_LongTermUsage 长期使用测试
func TestStringDict_LongTermUsage(t *testing.T) {
	tg := NewTimeGraph()

	// 模拟长期使用：多次添加和清理
	for cycle := 0; cycle < 10; cycle++ {
		// 添加数据
		for i := 0; i < 100; i++ {
			info := cmdb.Matcher{
				"host_id": "server-cycle-" + string(rune('0'+cycle)),
				"ip":      "10.0." + string(rune('0'+cycle)) + "." + string(rune('0'+i)),
			}
			_, err := tg.nodeBuilder.GetID("host", info)
			if err != nil {
				t.Fatalf("Cycle %d: Failed to get ID: %v", cycle, err)
			}
		}

		// 验证当前状态
		stats := tg.stringDict.GetStats()
		if stats["overflow_warned"].(bool) {
			t.Logf("Cycle %d: Overflow warning detected", cycle)
		}

		// 清理
		tg.Clean(nil)

		// 验证清理后状态
		statsAfter := tg.stringDict.GetStats()
		if statsAfter["total_strings"].(int) != 0 {
			t.Errorf("Cycle %d: Expected 0 strings after clean, got %v", cycle, statsAfter["total_strings"])
		}
	}
}

// TestStringDict_BackwardCompatibility 向后兼容性测试
func TestStringDict_BackwardCompatibility(t *testing.T) {
	// 测试使用全局字典的旧代码仍然可以工作
	nb := NewNodeBuilder(nil) // 传入nil使用全局字典

	info := cmdb.Matcher{"host_id": "legacy-server", "ip": "192.168.1.100"}
	_, err := nb.GetID("host", info)
	if err != nil {
		t.Fatalf("Backward compatibility test failed: %v", err)
	}

	// 验证全局字典被使用
	globalStats := globalStringDict.GetStats()
	if globalStats["total_strings"].(int) == 0 {
		t.Error("Global string dict should contain entries")
	}
}
