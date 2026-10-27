// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestFieldMapObservesMetadataStages(t *testing.T) {
	const indexBody = `{"metadata_observation":{"settings":{},"mappings":{"properties":{"trace_id":{"type":"keyword"}}}}}`
	const fallbackBody = `{"metadata_observation":{"mappings":{"properties":{"trace_id":{"type":"keyword"}}}}}`
	for _, test := range []struct {
		name          string
		indexStatus   int
		indexBody     string
		mappingStatus int
		mappingBody   string
		fallback      bool
		wantError     bool
		decodeCount   int64
		decodeBytes   int64
	}{
		{"index", 200, indexBody, 0, "", false, false, 1, int64(len(indexBody))},
		{"empty", 200, `{}`, 0, "", false, false, 1, 2},
		{"fallback", 500, `{}`, 200, fallbackBody, true, false, 1, int64(len(fallbackBody))},
		{"invalid_index_json", 200, `{`, 200, fallbackBody, true, false, 2, int64(len(fallbackBody) + 1)},
		{"fallback_failure", 500, `{}`, 403, `{}`, true, true, 0, 0},
		{"invalid_fallback_json", 500, `{}`, 200, `{`, true, true, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock.Init()
			ctx := metadata.InitHashID(context.Background())
			const alias = "metadata_observation"
			httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(*http.Request) (*http.Response, error) {
				time.Sleep(2 * time.Millisecond)
				return httpmock.NewStringResponse(test.indexStatus, test.indexBody), nil
			})
			var fallbackCalls int
			httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias+"/_mapping/", func(*http.Request) (*http.Response, error) {
				fallbackCalls++
				time.Sleep(2 * time.Millisecond)
				return httpmock.NewStringResponse(test.mappingStatus, test.mappingBody), nil
			})
			inst, err := NewInstance(ctx, &InstanceOption{Connect: Connect{Address: mock.EsUrl}, Timeout: time.Second})
			require.NoError(t, err)
			rec := setupESTraceRecorder(t)
			fields, indexes, err := inst.fieldMapWithPhysicalIndexes(ctx, nil, alias)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if test.name == "empty" {
					require.Empty(t, indexes)
					require.Empty(t, fields)
				} else {
					require.Equal(t, []string{alias}, indexes)
					require.Equal(t, "keyword", fields["trace_id"].FieldType)
				}
			}
			attrs := make(map[string]attribute.Value)
			for _, attr := range endedSpanAttrs(t, rec) {
				attrs[string(attr.Key)] = attr.Value
			}
			require.GreaterOrEqual(t, attrs["index-get-duration-ms"].AsFloat64(), 2.0)
			require.Contains(t, attrs, "mapping-client-duration-ms")
			require.Equal(t, test.decodeCount, attrs["mapping-decode-count"].AsInt64())
			require.Equal(t, test.decodeBytes, attrs["mapping-response-bytes"].AsInt64())
			require.GreaterOrEqual(t, attrs["mapping-decode-duration-ms"].AsFloat64(), 0.0)
			if test.fallback {
				require.Equal(t, 1, fallbackCalls)
				require.GreaterOrEqual(t, attrs["get-mapping-duration-ms"].AsFloat64(), 2.0)
			} else {
				require.Zero(t, fallbackCalls)
				require.NotContains(t, attrs, "get-mapping-duration-ms")
			}
			if test.wantError {
				require.NotContains(t, attrs, "mapping-field-parse-duration-ms")
			} else {
				require.Contains(t, attrs, "mapping-field-parse-duration-ms")
			}
		})
	}
}

func TestFieldMapObservesClientFailure(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	inst, err := NewInstance(ctx, &InstanceOption{Connect: Connect{Address: "://invalid"}, Timeout: time.Second})
	require.NoError(t, err)
	rec := setupESTraceRecorder(t)
	_, err = inst.fieldMap(ctx, nil, "metadata_observation")
	require.Error(t, err)
	attrs := make(map[string]attribute.Value)
	for _, attr := range endedSpanAttrs(t, rec) {
		attrs[string(attr.Key)] = attr.Value
	}
	require.Contains(t, attrs, "mapping-client-duration-ms")
	require.NotContains(t, attrs, "index-get-duration-ms")
	require.Zero(t, attrs["mapping-decode-count"].AsInt64())
}
