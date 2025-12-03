package trace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPrintfBehavior 测试fmt.Sprintf对复杂map的格式化行为
func TestPrintfBehavior(t *testing.T) {
	// 模拟实际的CausedBy map
	causedBy := map[string]interface{}{
		"type":        "too_many_buckets_exception",
		"reason":      "Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting.",
		"max_buckets": 65535,
	}

	// 测试fmt.Sprintf("%v", v)的行为
	for k, v := range causedBy {
		formatted := fmt.Sprintf("%v", v)
		t.Logf("字段: %s", k)
		t.Logf("原始长度: %d", len(fmt.Sprintf("%v", v)))
		t.Logf("格式化结果: %s", formatted)
		t.Logf("格式化长度: %d", len(formatted))
		t.Log("---")

		// 验证格式化是否截断
		if k == "reason" {
			expected := "Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting."
			assert.Equal(t, expected, formatted, "fmt.Sprintf应该完整保留reason字段")
			assert.Equal(t, len(expected), len(formatted), "reason字段长度应该保持一致")
		}
	}
}

// TestCompleteFlow 测试完整的错误处理流程（模拟实际场景）
func TestCompleteFlow(t *testing.T) {
	// 模拟ES返回的原始JSON
	originalJSON := `{"error":{"root_cause":[],"type":"search_phase_execution_exception","reason":"","phase":"fetch","grouped":true,"failed_shards":[],"caused_by":{"type":"too_many_buckets_exception","reason":"Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting.","max_buckets":65535}},"status":503}`

	// 模拟handleESSpecificError的处理
	causedBy := map[string]interface{}{
		"type":        "too_many_buckets_exception",
		"reason":      "Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting.",
		"max_buckets": 65535,
	}

	var msgBuilder strings.Builder
	msgBuilder.WriteString("caused by: \n")
	for k, v := range causedBy {
		msgBuilder.WriteString(fmt.Sprintf("%s: %v \n", k, v))
	}

	processedError := msgBuilder.String()

	t.Logf("原始JSON长度: %d", len(originalJSON))
	t.Logf("处理后错误长度: %d", len(processedError))
	t.Logf("处理后错误: %s", processedError)

	// 模拟"查询异常: caused by:"前缀（这是你看到的部分）
	finalError := "查询异常: " + processedError

	t.Logf("最终错误长度: %d", len(finalError))
	t.Logf("最终错误前100字符: %s", finalError[:min(100, len(finalError))])

	// 关键测试：检查是否真的在"查询异常: caused by:"之后就截断
	prefix := "查询异常: caused by:"
	assert.True(t, strings.HasPrefix(finalError, prefix), "错误应该以指定前缀开头")

	afterPrefix := finalError[len(prefix):]
	t.Logf("前缀之后的内容: %s", afterPrefix[:min(200, len(afterPrefix))])

	// 验证前缀之后确实有内容（而不是立即截断）
	assert.Greater(t, len(afterPrefix), 0, "前缀之后应该有更多内容")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}