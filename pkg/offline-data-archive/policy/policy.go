// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"

	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
)

func NewPolicy(
	ctx context.Context, meta *Meta, store stores.Store,
	check func() bool,
	log log.Logger,
) *Policy {
	p := &Policy{
		ctx: ctx,

		log: log,

		check: check,

		meta:  meta,
		store: store,
	}

	if p.check == nil {
		p.check = func() bool {
			return true
		}
	}

	return p
}

func (p *Policy) GetActiveShards(ctx context.Context, mapShards map[string]*shard.Shard) map[string]*shard.Shard {
	// 获取到所有活跃的shards
	var span oleltrace.Span
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "policy-get-archive-shards")

	if span != nil {
		defer span.End()
	}

	sds := p.store.GetActiveShards(ctx, p.meta.Database, mapShards)
	trace.InsertStringIntoSpan("database-name", p.meta.Database, span)
	trace.InsertIntIntoSpan("database-shards-num", len(sds), span)

	return sds
}
