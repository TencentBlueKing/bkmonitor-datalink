// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestPartitionProbeLoadIsMeasured(t *testing.T) {
	counter := cmdbTimeGraphLoadOperations.WithLabelValues("relation-partition", "container_with_pod_relation", "success")
	before := &dto.Metric{}
	require.NoError(t, counter.Write(before))
	CMDBTimeGraphLoadObserve(context.Background(), "relation-partition", "container_with_pod_relation", "success", time.Second, TimeGraphLoadSize{Returned: true, Series: 2, Points: 2})
	after := &dto.Metric{}
	require.NoError(t, counter.Write(after))
	require.Equal(t, before.GetCounter().GetValue()+1, after.GetCounter().GetValue())
	require.True(t, timeGraphStage("relation-partition"))
}

func TestTimeGraphMetricNamesBoundedUnderConcurrentSchemaChurn(t *testing.T) {
	names := timeGraphMetricNames{names: make(map[string]struct{})}
	require.Equal(t, "existing_relation", names.label("existing_relation"))
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 256; i++ {
				names.label(fmt.Sprintf("custom_%d_%d", worker, i))
			}
		}(worker)
	}
	workers.Wait()
	require.Len(t, names.names, maxTimeGraphLoadMetricNames)
	require.Equal(t, "existing_relation", names.label("existing_relation"))
	require.Equal(t, "__overflow__", names.label("new_relation"))
	require.Equal(t, "__overflow__", names.label(strings.Repeat("a", 257)))
	require.Equal(t, "__unknown__", names.label(""))
}
