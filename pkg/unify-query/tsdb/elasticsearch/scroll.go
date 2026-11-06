package elasticsearch

import (
	"context"
	"fmt"
	"net/http"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func (instance *Instance) ClearScroll(ctx context.Context, scrollIDs ...string) (err error) {
	ctx, span := trace.NewSpan(ctx, "elasticsearch-clear-scroll")
	defer span.End(&err)
	span.Set("scroll-count", len(scrollIDs))

	if len(scrollIDs) == 0 {
		return nil
	}
	for _, scrollID := range scrollIDs {
		if scrollID == "" || scrollID == "_all" {
			return fmt.Errorf("scroll cleanup requires explicit non-empty scroll IDs")
		}
	}

	client, err := instance.getClient(ctx, instance.connect,
		elastic.SetHealthcheck(false), elastic.SetRetrier(elastic.NewStopRetrier()))
	if err != nil {
		return err
	}
	defer client.Stop()

	// ES also returns 404 when every node fails to clear its contexts; inspect succeeded before treating it as idempotent.
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
	if !result.Succeeded {
		return fmt.Errorf("scroll cleanup failed: status=%d num_freed=%d", response.StatusCode, result.NumFreed)
	}
	if result.NumFreed == 0 {
		span.Set("already-cleared", true)
	}
	return nil
}
