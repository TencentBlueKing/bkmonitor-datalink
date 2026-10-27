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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestFieldMapObservationsDoNotCacheMetadata(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	var calls atomic.Int32
	const alias = "mapping_repeat_probe"
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return httpmock.NewStringResponse(http.StatusOK, `{"mapping_repeat_probe":{"settings":{},"mappings":{"properties":{"trace_id":{"type":"keyword"}}}}}`), nil
	})
	inst, err := NewInstance(ctx, &InstanceOption{Connect: Connect{Address: mock.EsUrl}, Timeout: time.Second})
	require.NoError(t, err)
	for range 2 {
		fields, err := inst.fieldMap(ctx, nil, alias)
		require.NoError(t, err)
		require.Contains(t, fields, "trace_id")
	}
	t.Logf("two identical fieldMap calls issue %d backend metadata requests", calls.Load())
	require.EqualValues(t, 2, calls.Load())
}
