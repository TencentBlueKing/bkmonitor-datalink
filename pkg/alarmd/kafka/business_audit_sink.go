package kafka

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// WriteBusinessAudit reuses the isolated Audit producer and its actual broker
// ACK. It does not commit a source offset or write business Topics.
func (s *ComparisonAuditKafkaSink) WriteBusinessAudit(ctx context.Context, a *comparator.BusinessAudit) error {
	if s == nil || s.core == nil {
		return ErrDecisionSinkClosed
	}
	payload, err := comparator.EncodeBusinessAudit(a, contract.MaxComparisonAuditBytesV1)
	if err != nil {
		return err
	}
	return s.core.writeEncoded(ctx, []byte(a.ID), payload)
}
