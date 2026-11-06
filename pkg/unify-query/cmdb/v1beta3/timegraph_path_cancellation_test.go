package v1beta3

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func longPathTestProvider() contractSchemaProvider {
	provider := contractSchemaProvider{primary: map[ResourceType][]string{}, fields: map[ResourceType][]string{}}
	for i := 0; i <= 12; i++ {
		name := ResourceType(fmt.Sprintf("r%d", i))
		provider.resources = append(provider.resources, name)
		provider.primary[name] = []string{"id"}
		provider.fields[name] = []string{"id"}
		if i > 0 {
			provider.schemas = append(provider.schemas, RelationSchema{RelationType: RelationType(fmt.Sprintf("edge%d", i)), Category: RelationCategoryStatic, FromType: provider.resources[i-1], ToType: name, MetricName: fmt.Sprintf("edge%d", i)})
		}
	}
	return provider
}

type cancellingPathProvider struct {
	contractSchemaProvider
	cancel context.CancelFunc
	calls  int
}

func (p *cancellingPathProvider) ListRelationSchemas(namespace string) []RelationSchema {
	p.calls++
	if p.calls == 3 {
		p.cancel()
	}
	return p.contractSchemaProvider.ListRelationSchemas(namespace)
}

func TestUnlimitedPathSearchHonorsCancellationDuringTraversal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &cancellingPathProvider{contractSchemaProvider: longPathTestProvider(), cancel: cancel}
	finder := NewPathFinder(WithSchemaProvider(provider), WithMaxHops(int(^uint(0)>>1)))
	paths, err := finder.FindAllPathsContext(ctx, "r0", "r12", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, paths)
	require.Equal(t, 3, provider.calls)
}
