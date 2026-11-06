// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Keep quality observable without extending the legacy instant/range wire format.
func observeLegacyRelationQuality(ctx context.Context, span *trace.Span, mode string, err error) {
	if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
		span.Set("relation-partial", true)
		span.Set("relation-partial-reason", status.Message)
		metric.CMDBRelationQualityInc(ctx, mode, "partial", status.Message)
	}
	var limitErr interface{ TruncationReason() string }
	if errors.As(err, &limitErr) {
		reason := limitErr.TruncationReason()
		span.Set("relation-truncated", true)
		span.Set("relation-truncated-reason", reason)
		metric.CMDBRelationQualityInc(ctx, mode, "truncated", reason)
	}
}
