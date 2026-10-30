package v1beta3

import (
	"context"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	uqredis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// A real host route must still be visible after an earlier relation consumes
// the metadata TTL. Routing uses IsSkipK8s for non-container result tables.
func TestTimeGraphPreparedRouteSurvivesMetadataExpiration(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	previous := viper.GetDuration(metadata.DefaultExpirationPath)
	viper.Set(metadata.DefaultExpirationPath, 20*time.Millisecond)
	metadata.InitMetadata()
	t.Cleanup(func() {
		viper.Set(metadata.DefaultExpirationPath, previous)
		metadata.InitMetadata()
	})
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, "expiry.host", &ir.ResultTableDetail{
		TableId: "expiry.host", VmRt: "expiry_host_rt", StorageId: 2,
		StorageType:     metadata.VictoriaMetricsStorageType,
		MeasurementType: uqredis.BKTraditionalMeasurement,
		Fields:          []string{"host_with_system_relation"},
	}))
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, "bkcc__expiry", &ir.Space{
		"expiry.host": {TableId: "expiry.host", Filters: []map[string]string{{"bk_biz_id": "expiry"}}},
	}))
	metadata.SetUser(ctx, &metadata.User{TenantID: "route-tenant", SpaceUID: "bkcc__expiry"})
	relation := cmdb.Relation{V: []cmdb.Resource{"host", "system"}, MetricName: "host_with_system_relation", RelationType: "host_with_system", Category: string(RelationCategoryStatic)}
	provider := contractSchemaProvider{
		resources: []ResourceType{"host", "system"},
		primary:   map[ResourceType][]string{"host": {"bk_host_id"}, "system": {"bk_target_ip"}},
		fields:    map[ResourceType][]string{"host": {"bk_host_id"}, "system": {"bk_target_ip"}},
	}
	model := &Model{schemaProvider: provider}
	fetches := 0
	model.timeGraphVMQuery = func(queryCtx context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		require.Equal(t, "route-tenant", metadata.GetUser(queryCtx).TenantID)
		require.True(t, metadata.GetQueryParams(queryCtx).IsSkipK8s)
		fetches++
		return nil, nil
	}
	loader := &timeGraphMatrixLoader{model: model, graph: &TimeGraph{nodeBuilder: newNodeBuilder(NewStringDict(), model.timeGraphConfig("bkcc__expiry"))}, start: time.Unix(1700000000, 0), end: time.Unix(1700000020, 0), step: 10 * time.Second, lookBack: time.Minute, topology: true}
	groups, err := loader.compileTopologyFetches(metadata.WithExactTimeGrid(ctx), "bkcc__expiry", []topologyFetchInput{{relation: relation}}, false)
	require.NoError(t, err, "physical host route exists at preparation")
	require.Len(t, groups, 1)
	time.Sleep(60 * time.Millisecond)
	_, _, err = loader.query(groups[0].ctx, groups[0].query)
	require.NoError(t, err, "an elapsed metadata TTL must not remove the physical route")
	require.Equal(t, 1, fetches)
}

// Later child queries must copy the original user after the parent TTL elapses.
func TestTimeGraphLaterSubqueryPreservesExpiredParentIdentity(t *testing.T) {
	mock.Init()
	previous := viper.GetDuration(metadata.DefaultExpirationPath)
	viper.Set(metadata.DefaultExpirationPath, 20*time.Millisecond)
	metadata.InitMetadata()
	t.Cleanup(func() {
		viper.Set(metadata.DefaultExpirationPath, previous)
		metadata.InitMetadata()
	})
	ctx := metadata.InitHashID(context.Background())
	parent := &metadata.User{Key: "source:parent", TenantID: "tenant", SpaceUID: "bkcc__7"}
	metadata.SetUser(ctx, parent)
	originalHashID := parent.HashID
	relation := cmdb.Relation{V: []cmdb.Resource{"node", "system"}, RelationType: "node_with_system", Category: string(RelationCategoryStatic)}
	calls := 0
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	query := func(child context.Context, _ *structured.QueryTs) (pl.Matrix, error) {
		calls++
		require.Equal(t, "tenant", metadata.GetUser(child).TenantID)
		require.Equal(t, "7", metadata.GetBkBizID(child))
		require.Equal(t, "source:parent", metadata.GetUser(child).Key)
		require.NotEqual(t, originalHashID, metadata.GetUser(child).HashID)
		if calls == 1 {
			time.Sleep(60 * time.Millisecond)
			require.Empty(t, metadata.GetUser(ctx).TenantID, "global parent cache really expired")
		}
		return contractMatrix(map[string]string{"node": "n1", "ip": "test-ip-1"}, 100000), nil
	}
	graph, err := model.buildTimeGraphFromRelationsWithQuery(ctx, "bkcc__7", time.Unix(100, 0), time.Unix(100, 0), time.Minute, "node", cmdb.Matcher{"node": "n1"}, nil, []cmdb.Relation{relation, relation}, "5m", query)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, originalHashID, parent.HashID)
	graph.Clean(ctx)
}
