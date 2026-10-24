package v1beta3

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func TestOwnedNodeCompressionRetainsLazyCopyMergeAndClean(t *testing.T) {
	provider := selectorProvider()
	provider.fields["a"] = []string{"a_id", "version", "empty"}
	graph := NewTimeGraphWithConfig((&Model{schemaProvider: provider}).timeGraphConfig("space"))
	n := graph.nodeBuilder
	n.deferCompression = true
	info := cmdb.Matcher{"a_id": "root", "version": "alpha", "empty": ""}
	id, err := n.GetID("a", info)
	require.NoError(t, err)
	require.Empty(t, n.compressedInfo)
	info["version"] = "caller-change"
	compressed := n.GetCompressedInfo(id)
	require.NotEmpty(t, compressed)
	require.Equal(t, cmdb.Matcher{"a_id": "root", "version": "alpha"}, n.GetOriginalInfoFromCompressed(compressed))
	for k := range compressed {
		delete(compressed, k)
	}
	require.NotEmpty(t, n.GetCompressedInfo(id), "compressed maps remain owned copies")
	again, err := n.GetID("a", cmdb.Matcher{"a_id": "root", "version": "zeta"})
	require.NoError(t, err)
	require.Equal(t, id, again)
	require.Empty(t, n.compressedInfo, "changed info invalidates private derived compression")
	require.Equal(t, cmdb.Matcher{"a_id": "root", "version": "zeta"}, n.GetOriginalInfoFromCompressed(n.GetCompressedInfo(id)))
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				_, e := n.GetID("a", cmdb.Matcher{"a_id": "root", "version": fmt.Sprintf("zz-%02d-%03d", i, j)})
				if e != nil {
					t.Error(e)
				}
				_ = n.GetCompressedInfo(id)
			}
		}(i)
	}
	workers.Wait()
	graph.Clean(context.Background())
	require.Nil(t, n.GetCompressedInfo(id))
	require.Zero(t, n.Length())
	eager := NewTimeGraphWithConfig((&Model{schemaProvider: provider}).timeGraphConfig("space"))
	defer eager.Clean(context.Background())
	id, err = eager.nodeBuilder.GetID("a", cmdb.Matcher{"a_id": "root", "version": "alpha"})
	require.NoError(t, err)
	require.NotEmpty(t, eager.nodeBuilder.compressedInfo[id], "default public builders retain eager dictionaries")
}
