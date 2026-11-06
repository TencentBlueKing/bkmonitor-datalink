package elasticsearch

import (
	"context"
	"fmt"

	elastic "github.com/olivere/elastic/v7"

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

	result, err := client.ClearScroll().ScrollId(scrollIDs...).Do(ctx)
	if elastic.IsNotFound(err) {
		span.Set("already-cleared", true)
		return nil
	}
	if err == nil {
		span.Set("num-freed", result.NumFreed)
		span.Set("succeeded", result.Succeeded)
		if !result.Succeeded && result.NumFreed > 0 {
			return fmt.Errorf("scroll cleanup partially failed: num_freed=%d", result.NumFreed)
		}
	}
	return err
}
