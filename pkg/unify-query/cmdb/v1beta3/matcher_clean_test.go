// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

// Verify the actual pool handoff without depending on sync.Pool.Get selection.
func TestTopologyNodeBuilderCleanClearsPooledMatcher(t *testing.T) {
	cfg := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "source", Index: cmdb.Index{"id"}, Info: cmdb.Index{"label"}}}}
	builder := newNodeBuilder(cfg)
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

func TestNodeBuilderOwnsAttributesDuringMergeAndClean(t *testing.T) {
	graph := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{
		Name: "entity", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version", "empty"},
	}}})
	n := graph.nodeBuilder
	info := cmdb.Matcher{"id": "root", "version": "alpha", "empty": ""}
	id, err := n.GetID("entity", info)
	require.NoError(t, err)
	info["version"] = "caller-change"
	_, original := n.Info(id)
	require.Equal(t, cmdb.Matcher{"id": "root", "version": "alpha", "empty": ""}, original)
	_, owned := n.Info(id)
	clear(owned)
	_, unchanged := n.Info(id)
	require.Equal(t, original, unchanged)
	again, err := n.GetID("entity", cmdb.Matcher{"id": "root", "version": "zeta"})
	require.NoError(t, err)
	require.Equal(t, id, again)
	_, updated := n.Info(id)
	require.Equal(t, cmdb.Matcher{"id": "root", "version": "zeta", "empty": ""}, updated)
	require.Equal(t, "alpha", original["version"], "updates must not mutate an exported map")
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				_, e := n.GetID("entity", cmdb.Matcher{"id": "root", "version": fmt.Sprintf("zz-%02d-%03d", i, j)})
				if e != nil {
					t.Error(e)
				}
				_, _ = n.Info(id)
			}
		}(i)
	}
	workers.Wait()
	_, merged := n.Info(id)
	require.Equal(t, "zz-07-099", merged["version"])
	graph.Clean(context.Background())
	_, missing := n.Info(id)
	require.Nil(t, missing)
	require.Zero(t, n.Length())
	require.Equal(t, "zz-07-099", merged["version"], "cleanup must not mutate an exported map")
}
