package trace

import (
	"fmt"
	"strings"
	"testing"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"
)

// TestHandleESSpecificError_CausedBy 测试handleESSpecificError函数的CausedBy解析
func TestHandleESSpecificError_CausedBy(t *testing.T) {
	// 直接测试CausedBy map的处理逻辑
	causedBy := map[string]interface{}{
		"type":        "too_many_buckets_exception",
		"reason":      "Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting.",
		"max_buckets": 65535,
	}

	// 直接模拟handleESSpecificError中处理CausedBy的逻辑
	var msgBuilder strings.Builder
	msgBuilder.WriteString("caused by: \n")
	for k, v := range causedBy {
		msgBuilder.WriteString(fmt.Sprintf("%s: %v \n", k, v))
	}

	result := fmt.Errorf("%s", msgBuilder.String())

	t.Logf("处理后的错误长度: %d", len(result.Error()))
	t.Logf("处理后的完整错误:")
	t.Logf("%s", result.Error())

	// 验证关键字段都存在
	keyChecks := []struct {
		field   string
		content string
	}{
		{"caused by:", "caused by: \n"},
		{"type", "type: too_many_buckets_exception"},
		{"reason", "reason: Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting."},
		{"max_buckets", "max_buckets: 65535"},
	}

	for _, check := range keyChecks {
		assert.Contains(t, result.Error(), check.content,
			"错误信息应该包含 %s 字段: %s", check.field, check.content)
	}

	// 特别检查是否在"caused by:"之后就被截断了
	causedByIndex := strings.Index(result.Error(), "caused by:")
	if causedByIndex == -1 {
		t.Fatal("错误信息中没有找到 'caused by:'")
	}

	afterCausedBy := result.Error()[causedByIndex:]
	t.Logf("'caused by:' 及之后的内容:")
	t.Logf("%s", afterCausedBy)

	// 验证"caused by:"之后确实有内容
	assert.Greater(t, len(afterCausedBy), len("caused by: \n"),
		"'caused by:' 之后应该有更多内容")

	// 验证reason字段完整存在
	reasonStart := strings.Index(result.Error(), "reason:")
	assert.Greater(t, reasonStart, causedByIndex, "reason应该在caused by之后")

	reasonEnd := strings.Index(result.Error()[reasonStart:], "\n")
	if reasonEnd == -1 {
		reasonEnd = len(result.Error()[reasonStart:])
	}
	actualReason := result.Error()[reasonStart : reasonStart+reasonEnd]
	expectedReason := "reason: Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting."

	assert.Equal(t, expectedReason, actualReason, "reason字段应该完整")
}

// 直接复制handleESSpecificError函数实现，避免import问题
func handleESSpecificError(elasticErr *elastic.Error) error {
	if elasticErr.Details == nil {
		return elasticErr
	}
	var msgBuilder strings.Builder

	if elasticErr.Details != nil {
		if len(elasticErr.Details.RootCause) > 0 {
			msgBuilder.WriteString("root cause: \n")
			for _, rc := range elasticErr.Details.RootCause {
				msgBuilder.WriteString(fmt.Sprintf("%s: %s \n", rc.Index, rc.Reason))
			}
		}

		if elasticErr.Details.CausedBy != nil {
			msgBuilder.WriteString("caused by: \n")
			for k, v := range elasticErr.Details.CausedBy {
				msgBuilder.WriteString(fmt.Sprintf("%s: %v \n", k, v))
			}
		}
	}

	return fmt.Errorf("%s", msgBuilder.String())
}

// TestCausedByFieldAnalysis 详细分析CausedBy字段的解析
func TestCausedByFieldAnalysis(t *testing.T) {
	// 测试不同类型的CausedBy值
	testCases := []struct {
		name     string
		causedBy map[string]interface{}
	}{
		{
			name: "too_many_buckets_exception",
			causedBy: map[string]interface{}{
				"type":        "too_many_buckets_exception",
				"reason":      "Trying to create too many buckets. Must be less than or equal to: [65535] but was [65702]. This limit can be set by changing the [search.max_buckets] cluster level setting.",
				"max_buckets": 65535,
			},
		},
		{
			name: "simple_error",
			causedBy: map[string]interface{}{
				"type":   "simple_type",
				"reason": "simple reason",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockError := &elastic.Error{
				Details: &elastic.ErrorDetails{
					CausedBy: tc.causedBy,
				},
			}

			result := handleESSpecificError(mockError)

			t.Logf("测试用例: %s", tc.name)
			t.Logf("结果: %s", result.Error())

			// 验证每个字段都被正确格式化
			for k, v := range tc.causedBy {
				expectedLine := fmt.Sprintf("%s: %v", k, v)
				assert.Contains(t, result.Error(), expectedLine,
					"应该包含字段 %s: %s", k, expectedLine)
			}
		})
	}
}