package kafka

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
)

type LegacyConvertedEvent = legacyoutput.Event

// LegacyEventConverter converts a group of events one by one: the events
// and errors it returns are aligned with the group, an event it will not
// write fails alone, and the third result is a failure that says nothing
// about the events (legacyoutput.SnapshotStoreError, a cancelled context).
type LegacyEventConverter interface {
	ConvertEach(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, []error, error)
}
