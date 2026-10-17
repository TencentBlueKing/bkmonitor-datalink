// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

// Verify the actual pool handoff without depending on sync.Pool.Get selection.
func TestTopologyNodeBuilderCleanClearsPooledMatcher(t *testing.T) {
	cfg := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "source", Index: cmdb.Index{"id"}, Info: cmdb.Index{"label"}}}}
	builder := NewNodeBuilderWithConfig(NewStringDict(), cfg)
	id, err := builder.GetID("source", cmdb.Matcher{"id": "a", "label": "original"})
	require.NoError(t, err)
	retainedInternalMap := builder.info[id]
	_, returnedDimensions := builder.Info(id)
	require.Equal(t, cmdb.Matcher{"id": "a", "label": "original"}, retainedInternalMap)

	builder.Clean()

	require.Empty(t, retainedInternalMap, "pool must not retain prior request labels")
	require.Zero(t, builder.Length())
	require.Equal(t, cmdb.Matcher{"id": "a", "label": "original"}, returnedDimensions, "owned output map must survive graph cleanup")
}
