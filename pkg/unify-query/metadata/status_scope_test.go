// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusScopeCopiesBaseAndIsolatesWrites(t *testing.T) {
	InitMetadata()
	ctx := InitHashID(context.Background())
	SetStatus(ctx, "ROUTE_PARTIAL", "route partial")

	first := WithStatusScope(ctx, 0)
	require.Equal(t, &Status{Code: "ROUTE_PARTIAL", Message: "route partial"}, GetStatus(first))
	SetStatus(first, "OUTPUT_ERROR", "output failed")

	require.Equal(t, &Status{Code: "OUTPUT_ERROR", Message: "output failed"}, GetStatus(first))
	require.Equal(t, &Status{Code: "ROUTE_PARTIAL", Message: "route partial"}, GetStatus(ctx))

	second := WithStatusScope(ctx, 1)
	require.Equal(t, &Status{Code: "ROUTE_PARTIAL", Message: "route partial"}, GetStatus(second))
}

func TestStatusWithoutScopeKeepsLegacyKey(t *testing.T) {
	InitMetadata()
	ctx := InitHashID(context.Background())
	SetStatus(ctx, "LEGACY", "legacy")
	require.Equal(t, &Status{Code: "LEGACY", Message: "legacy"}, GetStatus(ctx))
}

func TestSelectorStatusScopeIsolatesOutputWrites(t *testing.T) {
	InitMetadata()
	ctx := InitHashID(context.Background())
	output := WithStatusScope(ctx, 0)
	SetStatus(output, "ROUTE_PARTIAL", "route partial")

	selector := WithSelectorStatusScope(output)
	require.Equal(t, &Status{Code: "ROUTE_PARTIAL", Message: "route partial"}, GetStatus(selector))
	SetStatus(selector, QueryTsPartial, "selector partial")

	require.Equal(t, &Status{Code: QueryTsPartial, Message: "selector partial"}, GetStatus(selector))
	require.Equal(t, &Status{Code: "ROUTE_PARTIAL", Message: "route partial"}, GetStatus(output))
}
