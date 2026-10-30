package v1beta3

import (
	"context"
	"strconv"
	"testing"
	"time"

	kitlog "github.com/go-kit/log"
	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	promtsdb "github.com/prometheus/prometheus/tsdb"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqquery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	localprom "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// 路由是配置输入；QueryReference、表达式、时间窗口和返回时间桶均由生产代码生成。
// 用真实 TSDB 原始样本运行生产 Prometheus Instance，不预制 Matrix 查询结果。
func TestLegacyRangeStorageGrid(t *testing.T) {
	const start = int64(1793259612)
	const end = start + 3600
	for _, tc := range []struct {
		name        string
		vmMetric    string
		oldVMRoute  bool
		emptyDirect bool
		expand      bool
		showInfo    bool
		rootOnly    bool
		invalidInfo bool
		wantStart   int64
		wantEnd     int64
		sampleTime  int64
		wantPath    []string
	}{
		{name: "local_last_sample", wantStart: start, wantEnd: end, sampleTime: end - 7, wantPath: []string{"source", "middle", "target"}},
		{name: "vm_on_first_edge", vmMetric: "source_middle_flow", wantStart: start - 12, wantEnd: end - 12, sampleTime: end - 20, wantPath: []string{"source", "middle", "target"}},
		{name: "vm_on_second_edge", vmMetric: "middle_target_flow", wantStart: start - 12, wantEnd: end - 12, sampleTime: end - 20, wantPath: []string{"source", "middle", "target"}},
		{name: "empty_vm_candidate_then_local", vmMetric: "source_target_flow", emptyDirect: true, wantStart: start, wantEnd: end, sampleTime: end - 7, wantPath: []string{"source", "middle", "target"}},
		{name: "vm_outside_original_route_window", oldVMRoute: true, wantStart: start, wantEnd: end, sampleTime: end - 7, wantPath: []string{"source", "middle", "target"}},
		{name: "source_filter_uses_vm", vmMetric: "source_info_relation", expand: true, wantStart: start - 12, wantEnd: end - 12, sampleTime: end - 20, wantPath: []string{"source", "middle", "target"}},
		{name: "target_projection_uses_vm", vmMetric: "target_info_relation", showInfo: true, wantStart: start - 12, wantEnd: end - 12, sampleTime: end - 20, wantPath: []string{"source", "middle", "target"}},
		{name: "intermediate_projection_does_not_select_grid", vmMetric: "middle_info_relation", showInfo: true, wantStart: start, wantEnd: end, sampleTime: end - 7, wantPath: []string{"source", "middle", "target"}},
		{name: "local_root_projection", rootOnly: true, showInfo: true, wantStart: start, wantEnd: end, sampleTime: end - 7, wantPath: []string{"source"}},
		{name: "vm_root_projection", rootOnly: true, showInfo: true, vmMetric: "source_info_relation", wantStart: start - 12, wantEnd: end - 12, sampleTime: end - 20, wantPath: []string{"source"}},
		{name: "optional_info_preparation_failure", invalidInfo: true, showInfo: true, wantStart: start, wantEnd: end, sampleTime: end - 7, wantPath: []string{"source", "middle", "target"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			schemas := []relation.RelationSchema{
				{RelationName: "source_to_middle", FromType: "source", ToType: "middle", Category: relation.RelationCategoryStatic, IsDirectional: true, MetricName: "source_middle_flow"},
				{RelationName: "middle_to_target", FromType: "middle", ToType: "target", Category: relation.RelationCategoryStatic, IsDirectional: true, MetricName: "middle_target_flow"},
			}
			if tc.emptyDirect {
				schemas = append(schemas, relation.RelationSchema{RelationName: "source_to_target", FromType: "source", ToType: "target", Category: relation.RelationCategoryStatic, IsDirectional: true, MetricName: "source_target_flow"})
			}
			provider := NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
				ResourcePrimaryKeys: map[string][]string{"source": {"source_id"}, "middle": {"middle_id"}, "target": {"target_id"}},
				ResourceDefinitions: []*relation.ResourceDefinition{
					{Name: "source", Fields: []relation.FieldDefinition{{Name: "source_id", Required: true}, {Name: "region"}}},
					{Name: "middle", Fields: []relation.FieldDefinition{{Name: "middle_id", Required: true}, {Name: "region"}}},
					{Name: "target", Fields: []relation.FieldDefinition{{Name: "target_id", Required: true}, {Name: "region"}}},
				},
				RelationSchemas: schemas,
			}))
			m := &Model{schemaProvider: provider}
			m.SetTimeGraphResolver(func(context.Context, string) (TimeGraphQuerier, error) { return m, nil })
			m.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
				q.TsDBMap = make(map[string]structured.TsDBs)
				for _, metric := range q.QueryList {
					storageType := metadata.InfluxDBStorageType
					if metric.FieldName == tc.vmMetric {
						storageType = metadata.VictoriaMetricsStorageType
					}
					route := &uqquery.TsDBV2{
						TableID: "grid." + metric.FieldName, Field: []string{metric.FieldName},
						MeasurementType: redis.BkSplitMeasurement, ExpandMetricNames: []string{"a"},
						StorageID: "1", StorageType: storageType, DB: "grid", Measurement: metric.FieldName,
					}
					if tc.oldVMRoute {
						// 原始范围连同路由的 1h 重叠只命中 Influx；提前 12s 则会误命中旧 VM。
						route.StorageClusterRecords = uqquery.StorageClusterRecords{
							{StorageID: "1", StorageType: metadata.InfluxDBStorageType, DB: "grid", Measurement: metric.FieldName, EnableTime: start - 3606},
							{StorageID: "2", StorageType: metadata.VictoriaMetricsStorageType, DB: "grid", Measurement: metric.FieldName, EnableTime: start - 7200},
						}
					}
					if tc.invalidInfo && metric.FieldName == "target_info_relation" {
						route.StorageID, route.StorageType = "grid-test-missing-storage", ""
					}
					q.TsDBMap[metric.ReferenceName] = structured.TsDBs{route}
				}
				return q.ToQueryReference(ctx)
			}
			engine := pl.NewEngine(pl.EngineOpts{Timeout: 5 * time.Second, MaxSamples: 10000})
			instances := make(map[string]*localprom.Instance)
			metrics := []string{"source_info_relation", "middle_info_relation", "target_info_relation"}
			for _, schema := range schemas {
				metrics = append(metrics, schema.MetricName)
			}
			for _, metric := range metrics {
				db, err := promtsdb.Open(t.TempDir(), kitlog.NewNopLogger(), nil, promtsdb.DefaultOptions(), nil)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				if metric != "source_target_flow" {
					appender := db.Appender(ctx)
					_, err = appender.Append(0, labels.FromStrings("__name__", "a", "source_id", "a", "middle_id", "b", "target_id", "c", "region", "r"), tc.sampleTime*1000, 1)
					require.NoError(t, err)
					require.NoError(t, appender.Commit())
				}
				instances[metric] = localprom.NewInstance(ctx, engine, db, time.Minute, 2)
			}
			var queried []string
			m.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, actualStart, actualEnd time.Time, step time.Duration) (pl.Matrix, error) {
				metric := q.QueryList[0].FieldName
				queried = append(queried, metric)
				wantStart, wantEnd := tc.wantStart, tc.wantEnd
				if metric == "source_target_flow" {
					wantStart, wantEnd = start-12, end-12
				}
				require.False(t, instant)
				require.Equal(t, wantStart, actualStart.Unix(), metric)
				require.Equal(t, wantEnd, actualEnd.Unix(), metric)
				t.Logf("%s: %s [%d,%d]", metric, expr, actualStart.Unix(), actualEnd.Unix())
				matrix, _, err := instances[metric].DirectQueryRange(ctx, expr, actualStart, actualEnd, step)
				return matrix, err
			}
			var expand cmdb.Matcher
			if tc.expand {
				expand = cmdb.Matcher{"region": "r"}
			}
			target := cmdb.Resource("target")
			wantMatcher := cmdb.Matcher{"target_id": "c"}
			if tc.rootOnly {
				target = "source"
				wantMatcher = cmdb.Matcher{"source_id": "a"}
			}
			if tc.showInfo && !tc.invalidInfo {
				wantMatcher["region"] = "r"
			}
			_, _, path, _, series, err := m.QueryResourceMatcherRange(ctx, "1m", "space", "60s", strconv.FormatInt(start, 10), strconv.FormatInt(end, 10), target, "source", cmdb.Matcher{"source_id": "a"}, expand, tc.showInfo, nil)
			require.NoError(t, err)
			require.Equal(t, tc.wantPath, path)
			require.Equal(t, []cmdb.MatchersWithTimestamp{{Timestamp: tc.wantEnd * 1000, Matchers: cmdb.Matchers{wantMatcher}}}, series)
			if tc.invalidInfo {
				require.NotNil(t, metadata.GetStatus(ctx))
				require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(ctx).Code)
				require.Contains(t, metadata.GetStatus(ctx).Message, "target_info_failed")
			} else {
				require.Nil(t, metadata.GetStatus(ctx))
			}
			if tc.emptyDirect {
				require.Equal(t, []string{"source_target_flow", "source_middle_flow", "middle_target_flow"}, queried)
			}
		})
	}
}

func TestLegacyRangeRootWithoutInfoSkipsRouting(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	m := &Model{schemaProvider: NewStaticSchemaProvider()}
	m.SetTimeGraphResolver(func(context.Context, string) (TimeGraphQuerier, error) { return m, nil })
	m.timeGraphQueryReference = func(context.Context, *structured.QueryTs) (metadata.QueryReference, error) {
		t.Fatal("invalid root projection must fail before route preparation")
		return nil, nil
	}
	_, _, _, _, _, err := m.QueryResourceMatcherRange(ctx, "1m", "space", "60s", "1793259612", "1793263212", "pod", "pod", cmdb.Matcher{"pod": "example"}, nil, false, nil)
	require.ErrorContains(t, err, "表达式配置不能为空")
	require.Nil(t, metadata.GetStatus(ctx))
}
