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
		path   string
		want   string
	}{
		{"max_topology_nodes", "source.target", "topology resource limit exceeded: max_topology_nodes (12 > 10)"},
		{"max_response_bytes", "", "topology resource limit exceeded: max_response_bytes (12 > 10)"},
		{"max_targets", "source.target", "result limit exceeded: source.target returned 12 items, maximum is 10"},
		{"max_targets", "", "result limit exceeded: query returned 12 targets, maximum is 10"},
	} {
		t.Run(test.reason+"/"+test.path, func(t *testing.T) {
			limit := &ResultLimitError{Reason: test.reason, Count: 12, Limit: 10, Path: test.path}
			require.Equal(t, test.want, limit.Error())
			require.Equal(t, test.reason, limit.TruncationReason())
			require.Equal(t, metric.CMDBRelationResultRejected, metric.CMDBTimeGraphErrorResult(limit))
			require.Equal(t, metric.CMDBRelationResultRejected, metric.CMDBTimeGraphErrorResult(fmt.Errorf("query: %w", limit)))
		})
	}
}
