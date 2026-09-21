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
