package structured

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/stretchr/testify/require"
)

func TestQueryFieldSemantics(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.GetQueryParams(ctx).SetTime(time.Unix(1000, 0), time.Unix(1000, 0), time.Unix(2000, 0), time.Minute, "s", "UTC")
	for _, tc := range []struct {
		semantics, storage string
		wantError          bool
	}{
		{metadata.FTAEventTagsV1, metadata.ElasticsearchStorageType, false},
		{"", metadata.ElasticsearchStorageType, false},
		{"fta_event_tags/v2", metadata.ElasticsearchStorageType, true},
		{metadata.FTAEventTagsV1, metadata.InfluxDBStorageType, true},
		{metadata.FTAEventTagsV1, metadata.BkSqlStorageType, true},
	} {
		t.Run(tc.semantics+tc.storage, func(t *testing.T) {
			var q Query
			require.NoError(t, json.Unmarshal([]byte(`{"field_semantics":"`+tc.semantics+`","data_source":"bk_monitor","table_id":"fta.event","field_name":"time","reference_name":"a"}`), &q))
			metric, err := q.ToQueryMetric(ctx, "bkcc__2", TsDBs{{TableID: "fta.event", StorageID: "1", StorageType: tc.storage, DB: "bkfta_event_*_read", Measurement: "__default__"}})
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, metric.QueryList, 1)
			require.Equal(t, tc.semantics, metric.QueryList[0].FieldSemantics)
		})
	}
}

func TestQuerySourceConditions(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.GetQueryParams(ctx).SetTime(time.Unix(1000, 0), time.Unix(1000, 0), time.Unix(2000, 0), time.Minute, "s", "UTC")
	var q Query
	require.NoError(t, json.Unmarshal([]byte(`{"field_semantics":"fta_event_tags/v1","source_conditions":{"field_list":[{"field_name":"status","op":"eq","value":["ABNORMAL"]}]},"conditions":{"field_list":[{"field_name":"tags.env","op":"contains","value":["prod"]}]},"data_source":"bk_monitor","table_id":"fta.event","field_name":"time","reference_name":"a"}`), &q))
	metric, err := q.ToQueryMetric(ctx, "bkcc__2", TsDBs{{TableID: "fta.event", StorageID: "1", StorageType: metadata.ElasticsearchStorageType, DB: "bkfta_event_*_read", Measurement: "__default__"}})
	require.NoError(t, err)
	require.Len(t, metric.QueryList, 1)
	require.Equal(t, "status", metric.QueryList[0].SourceConditions[0][0].DimensionName)
	require.Equal(t, "tags.env", metric.QueryList[0].AllConditions[0][0].DimensionName)
	q.FieldSemantics = ""
	_, err = q.ToQueryMetric(ctx, "bkcc__2", nil)
	require.ErrorContains(t, err, "source_conditions requires")
}

func TestQuerySourceConditionsInvalidOffsetDoesNotPanic(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.GetQueryParams(ctx).SetTime(time.Unix(1000, 0), time.Unix(1000, 0), time.Unix(2000, 0), time.Minute, "s", "UTC")
	for _, semantics := range []string{"", metadata.FTAEventTagsV1} {
		q := Query{FieldSemantics: semantics, TableID: "fta.event", FieldName: "time", ReferenceName: "a", Offset: "invalid"}
		metric, err := q.ToQueryMetric(ctx, "bkcc__2", TsDBs{{TableID: "fta.event", StorageID: "1", StorageType: metadata.ElasticsearchStorageType, DB: "bkfta_event_*_read", Measurement: "__default__"}})
		require.NoError(t, err)
		require.Empty(t, metric.QueryList)
	}
}
