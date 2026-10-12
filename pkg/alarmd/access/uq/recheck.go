// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Recheck runs a frozen physical query again, as the lookback's later read
// of the same window. It is the formal read's request, normalization and
// response limits exactly -- so a series and a record read now carry the
// identity and record id they carried then -- with no Slot, no Operation and
// no recovery permit: the caller holds the lookback's own share of the query
// budget and bounds the call with ctx.
func (client *Client) Recheck(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	if client == nil || sink == nil {
		return execution.ProviderCompletion{}, errors.New("alarmd access uq: initialized client and sink are required")
	}
	if spec.Digest == "" {
		return execution.ProviderCompletion{}, errors.New("alarmd access uq: a frozen physical query is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return execution.ProviderCompletion{}, errors.New("alarmd access uq: a recheck needs a deadline")
	}
	return client.execute(ctx, ctx, queryIdentity{Spec: spec, AttemptNo: 1}, sink, nil)
}
