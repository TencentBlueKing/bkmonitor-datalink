package kafka

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
)

type LegacyConvertedEvent = legacyoutput.Event
type LegacyEventConverter interface {
	ConvertBatch(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error)
}
