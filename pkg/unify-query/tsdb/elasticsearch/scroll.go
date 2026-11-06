package elasticsearch

import (
	"context"
	"fmt"
	"net/http"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// ClearScroll 释放显式指定的游标，并校验 ES 返回的实际清理结果。
func (instance *Instance) ClearScroll(ctx context.Context, scrollIDs ...string) (err error) {
	ctx, span := trace.NewSpan(ctx, "elasticsearch-clear-scroll")
	defer span.End(&err)
	span.Set("scroll-count", len(scrollIDs))

	if len(scrollIDs) == 0 {
		return nil
	}
	// 禁止 _all，确保本次回收只涉及当前会话保存的游标，不影响其他下载会话。
	for _, scrollID := range scrollIDs {
		if scrollID == "" || scrollID == "_all" {
			return fmt.Errorf("scroll cleanup requires explicit non-empty scroll IDs")
		}
	}

	// 复用查询使用的地址、网关头和 Basic Auth；关闭健康检查和自动重试，控制收尾耗时。
	client, err := instance.getClient(ctx, instance.connect,
		elastic.SetHealthcheck(false), elastic.SetRetrier(elastic.NewStopRetrier()))
	if err != nil {
		return err
	}
	defer client.Stop()

	// ES 在无游标可释放和节点清理全部失败时都可能返回 404，不能仅凭状态码判定幂等成功。
	// 保留原始响应并检查 succeeded，才能识别节点失败、网关 404 或无效响应。
	response, err := client.PerformRequest(ctx, elastic.PerformRequestOptions{
		Method:       http.MethodDelete,
		Path:         "/_search/scroll/",
		Body:         map[string][]string{"scroll_id": scrollIDs},
		IgnoreErrors: []int{http.StatusNotFound},
	})
	if err != nil {
		return err
	}
	var result elastic.ClearScrollResponse
	if err := json.Unmarshal(response.Body, &result); err != nil {
		return fmt.Errorf("decode scroll cleanup response: %w", err)
	}
	span.Set("num-freed", result.NumFreed)
	span.Set("succeeded", result.Succeeded)
	// 即使释放了部分游标，succeeded=false 仍表示本次清理失败。
	if !result.Succeeded {
		return fmt.Errorf("scroll cleanup failed: status=%d num_freed=%d", response.StatusCode, result.NumFreed)
	}
	// 成功且零释放量通常表示游标已过期或已清理，重复调用仍视为成功。
	if result.NumFreed == 0 {
		span.Set("already-cleared", true)
	}
	return nil
}
