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

	elastic "github.com/olivere/elastic/v7"
)

type partialShardResultsKey struct{}

// WithPartialShardResults enables accepting partial shard responses and
// counts successfully decoded partial results across one request.
func WithPartialShardResults(ctx context.Context) (context.Context, *atomic.Uint32) {
	count := &atomic.Uint32{}
	return context.WithValue(ctx, partialShardResultsKey{}, count), count
}

func partialShardResults(ctx context.Context) *atomic.Uint32 {
	count, _ := ctx.Value(partialShardResultsKey{}).(*atomic.Uint32)
	return count
}

func hasFailedSearchShards(result *elastic.SearchResult) bool {
	return result != nil && result.Shards != nil && (result.Shards.Failed > 0 || len(result.Shards.Failures) > 0)
}

func canAcceptPartialShardResult(result *elastic.SearchResult) bool {
	if !hasFailedSearchShards(result) || result.Error != nil || result.TimedOut {
		return false
	}
	// A successful search can omit the status field.
	successfulStatus := result.Status == 0 || (result.Status >= http.StatusOK && result.Status < http.StatusMultipleChoices)
	// ES includes skipped shards in successful, even though they did not execute.
	return successfulStatus && result.Shards.Successful > result.Shards.Skipped
}

func recordPartialShardResult(ctx context.Context, result *elastic.SearchResult) {
	if count := partialShardResults(ctx); count != nil && canAcceptPartialShardResult(result) {
		count.Add(1)
	}
}
