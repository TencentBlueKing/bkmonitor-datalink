package tools

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
)

func StartListenerFromFile(ctx context.Context, filePath string) error {
	processor, err := pre_calculate.Initial(ctx)
	if err != nil {
		return err
	}
	if err := core.CreateMockMetadataCenter(); err != nil {
		return err
	}

	processor.RunWithStandLone(filePath)
	return nil
}
