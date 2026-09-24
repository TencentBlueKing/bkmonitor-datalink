// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

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
