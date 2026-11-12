// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

func TestResultLimitErrorMessageAndClassification(t *testing.T) {
	for _, test := range []struct {
		reason string
		want   string
	}{
		{"max_topology_queries", "topology resource limit exceeded: max_topology_queries (12 > 10)"},
		{"max_topology_request_bytes", "topology resource limit exceeded: max_topology_request_bytes (12 > 10)"},
		{"max_response_bytes", "topology resource limit exceeded: max_response_bytes (12 > 10)"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			limit := &ResultLimitError{Reason: test.reason, Count: 12, Limit: 10}
			require.Equal(t, test.want, limit.Error())
			require.Equal(t, test.reason, limit.TruncationReason())
			require.Equal(t, metric.CMDBRelationResultRejected, metric.CMDBTimeGraphErrorResult(limit))
			require.Equal(t, metric.CMDBRelationResultRejected, metric.CMDBTimeGraphErrorResult(fmt.Errorf("query: %w", limit)))
		})
	}
}
