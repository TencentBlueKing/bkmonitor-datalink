package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestTimeGraphRedisSchemaIsolationAndReload(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	write := func(kind, namespace string, definitions any) {
		t.Helper()
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+kind, namespace, data).Err())
	}
	resource := func(namespace, field string) map[string]*relation.ResourceDefinition {
		return map[string]*relation.ResourceDefinition{"service": {
			Namespace: namespace, Name: "service", Fields: []relation.FieldDefinition{{Name: field, Required: true}},
		}}
	}
	relations := func(namespace, metric string) map[string]*relation.RelationDefinition {
		return map[string]*relation.RelationDefinition{"calls": {
			Namespace: namespace, Name: "calls", FromResource: "service", ToResource: "service",
			Category: "dynamic", IsDirectional: true, Labels: map[string]string{"metric_name": metric},
		}}
	}
	for _, ns := range []string{relation.NamespaceAll, "tenant_a", "tenant_b"} {
		write(relation.KindResourceDefinition, ns, resource(ns, ns+"_id"))
		write(relation.KindRelationDefinition, ns, relations(ns, ns+"_flow"))
	}
	provider, err := relation.NewRedisProvider(ctx, client)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	adapter := NewSchemaProviderFromRelation(provider)
	model := &Model{schemaProvider: adapter, timeGraphQueryReference: timeGraphTestQueryReference}
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, instant bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		query := q.QueryList[0]
		var field, target string
		switch query.FieldName {
		case "__all___flow":
			field, target = "__all___id", "global"
		case "tenant_a_flow":
			field, target = "tenant_a_id", "a"
		case "tenant_a_flow_v2":
			field, target = "tenant_a_id", "a-v2"
		case "tenant_b_flow":
			field, target = "tenant_b_id", "b"
		default:
			return nil, fmt.Errorf("unexpected metric %s", query.FieldName)
		}
		timestamps := []int64{1700000000000}
		if !instant {
			timestamps = append(timestamps, 1700000060000)
		}
		matrix := contractMatrix(map[string]string{"from_" + field: "root", "to_" + field: target}, timestamps...)
		return filteredTimeGraphMatrix(t, matrix, query.Conditions), nil
	}
	query := func(namespace, field string, ranged bool) ([]cmdb.PathResourcesResult, error) {
		if ranged {
			return model.QueryPathResourcesRange(ctx, "5m", namespace, "1m", "1700000000", "1700000060", "service", []cmdb.Resource{"service"}, [][]cmdb.Resource{{"service", "service"}}, cmdb.Matcher{field: "root"})
		}
		return model.QueryPathResources(ctx, "5m", namespace, "1700000000", "service", []cmdb.Resource{"service"}, [][]cmdb.Resource{{"service", "service"}}, cmdb.Matcher{field: "root"})
	}
	check := func(namespace, field, target string) {
		t.Helper()
		for _, ranged := range []bool{false, true} {
			results, err := query(namespace, field, ranged)
			require.NoError(t, err)
			count := 1
			if ranged {
				count = 2
			}
			require.Len(t, results, count)
			for _, result := range results {
				require.Equal(t, cmdb.Matcher{field: target}, result.Path[1].Dimensions)
			}
		}
	}
	check("unknown_tenant", "__all___id", "global")
	check("tenant_a", "tenant_a_id", "a")
	check("tenant_b", "tenant_b_id", "b")

	channel := relation.RedisKeyPrefix + ":" + relation.KindRelationDefinition + relation.DefaultRedisPubSubChannelSuffix
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, channel).Result()
		return err == nil && counts[channel] == 1
	}, time.Second*3, time.Millisecond*10)
	updated := make(chan struct{}, 1)
	require.NoError(t, provider.Subscribe(func(kind, namespace string) {
		if kind == relation.KindRelationDefinition && namespace == "tenant_a" {
			select {
			case updated <- struct{}{}:
			default:
			}
		}
	}))
	write(relation.KindRelationDefinition, "tenant_a", relations("tenant_a", "tenant_a_flow_v2"))
	payload, err := json.Marshal(relation.MsgPayload{Namespace: "tenant_a", Kind: relation.KindRelationDefinition})
	require.NoError(t, err)
	require.NoError(t, client.Publish(ctx, channel, payload).Err())
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("schema reload did not complete")
	}
	check("tenant_a", "tenant_a_id", "a-v2")
	check("tenant_b", "tenant_b_id", "b")
	check("unknown_tenant", "__all___id", "global")

	// Concurrent requests must retain the namespace-specific identity and metric.
	var workers sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			namespace, field, target := "tenant_a", "tenant_a_id", "a-v2"
			if i%2 == 1 {
				namespace, field, target = "tenant_b", "tenant_b_id", "b"
			}
			for repeat := 0; repeat < 10; repeat++ {
				results, err := query(namespace, field, false)
				if err != nil {
					errors <- err
					return
				}
				if len(results) != 1 || results[0].Path[1].Dimensions[field] != target {
					errors <- fmt.Errorf("namespace %s leaked result: %+v", namespace, results)
					return
				}
			}
		}(i)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func TestTimeGraphBusinessRelationNameMatchesStaticAndRedis(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	write := func(kind string, definitions any) {
		t.Helper()
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+kind, relation.NamespaceAll, data).Err())
	}
	write(relation.KindResourceDefinition, map[string]*relation.ResourceDefinition{
		"business": {Namespace: relation.NamespaceAll, Name: "business", Fields: []relation.FieldDefinition{{Name: "bk_biz_id", Required: true, Type: relation.FieldTypeInteger}}},
		"set":      {Namespace: relation.NamespaceAll, Name: "set", Fields: []relation.FieldDefinition{{Name: "bk_set_id", Required: true, Type: relation.FieldTypeInteger}}},
	})
	write(relation.KindRelationDefinition, map[string]*relation.RelationDefinition{
		"business_with_set": {Namespace: relation.NamespaceAll, Name: "business_with_set", FromResource: "business", ToResource: "set"},
	})
	provider, err := relation.NewRedisProvider(ctx, client)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	staticModel := &Model{schemaProvider: NewStaticSchemaProvider()}
	redisModel := &Model{schemaProvider: NewSchemaProviderFromRelation(provider)}
	wantRelations := staticModel.sharedTopologyRelations("bkcc__2", nil, []string{"business_with_set"}, DirectionBoth)
	require.Len(t, wantRelations, 2)
	require.Equal(t, wantRelations, redisModel.sharedTopologyRelations("bkcc__2", nil, []string{"business_with_set"}, DirectionBoth))
	for _, item := range wantRelations {
		require.Equal(t, "business_with_set", item.RelationType)
	}

	for _, fixture := range []struct {
		name  string
		model *Model
	}{
		{name: "static", model: staticModel},
		{name: "redis", model: redisModel},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			model := fixture.model
			// The retired spelling must not select the relation through an alias.
			require.Empty(t, model.sharedTopologyRelations("bkcc__2", nil, []string{"business_set"}, DirectionBoth))
			model.timeGraphQueryReference = timeGraphTestQueryReference
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, _ time.Duration) (pl.Matrix, error) {
				require.Equal(t, "business_with_set_relation", q.QueryList[0].FieldName)
				var timestamps []int64
				for timestamp := start; !timestamp.After(end); timestamp = timestamp.Add(time.Minute) {
					timestamps = append(timestamps, timestamp.UnixMilli())
				}
				matrix := contractMatrix(map[string]string{"bk_biz_id": "2", "bk_set_id": "3"}, timestamps...)
				return filteredTimeGraphMatrix(t, matrix, q.QueryList[0].Conditions), nil
			}
			for _, format := range []string{"snapshots", cmdb.CompactTopologyFormat} {
				for _, pointCount := range []int{1, 3} {
					t.Run(fmt.Sprintf("%s/%d-points", format, pointCount), func(t *testing.T) {
						request := cmdb.SharedTopologyQuery{
							SpaceUID: "bkcc__2", Timestamp: 1700000000, SourceType: "business",
							SourceInfo: cmdb.Matcher{"bk_biz_id": "2"}, MaxHops: 1,
							AllowedRelationTypes: []string{"business_with_set"}, ResponseFormat: format,
						}
						if pointCount > 1 {
							request.Timestamp = 0
							request.StartTime, request.EndTime, request.Step = 1700000000, 1700000120, "60s"
						}
						result, err := model.QuerySharedTopology(ctx, request)
						require.NoError(t, err)
						require.Equal(t, pointCount, result.PointCount)
						snapshots := result.Snapshots
						if format == cmdb.CompactTopologyFormat {
							require.NotNil(t, result.Compact)
							require.Equal(t, 2*pointCount, result.Compact.NodeOccurrences)
							require.Equal(t, 2*pointCount, result.Compact.EdgeOccurrences)
							snapshots = decodeCompactForTest(t, result.Compact)
						}
						require.Len(t, snapshots, pointCount)
						for i, snapshot := range snapshots {
							require.Equal(t, int64(1700000000000+i*60000), snapshot.Timestamp)
							require.Len(t, snapshot.Nodes, 2)
							require.Len(t, snapshot.Edges, 2)
							require.False(t, snapshot.Partial)
							for _, edge := range snapshot.Edges {
								require.Equal(t, "business_with_set", edge.RelationType)
							}
						}
					})
				}
			}
		})
	}
}
