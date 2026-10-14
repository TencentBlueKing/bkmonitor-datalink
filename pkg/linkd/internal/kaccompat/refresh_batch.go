// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kaccompat

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/projection"
)

type refreshRound struct {
	done chan struct{}
	err  error
}

// refreshIndex 合并同索引已经完成的写入，刷新开始后到达的写入进入下一轮。
// 不复用进行中的刷新来确认较晚写入，避免回执宣称尚未获得的搜索可见性。
func (c *Client) refreshIndex(ctx context.Context, index string) error {
	c.refreshMu.Lock()
	if round := c.refreshes[index]; round != nil {
		c.refreshMu.Unlock()
		select {
		case <-round.done:
			return round.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	round := &refreshRound{done: make(chan struct{})}
	if c.refreshes == nil {
		c.refreshes = map[string]*refreshRound{}
	}
	c.refreshes[index] = round
	c.refreshMu.Unlock()
	// 仅开启实际合批时收集；普通单元客户端保留立即刷新行为。
	if c.batch != nil {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	c.refreshMu.Lock()
	delete(c.refreshes, index)
	c.refreshMu.Unlock()
	var result struct {
		Shards *struct {
			Total  int `json:"total"`
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	round.err = c.request(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_refresh", nil, nil, &result)
	if round.err == nil && (result.Shards == nil || result.Shards.Total < 1 || result.Shards.Failed != 0) {
		round.err = projection.Failure{Code: "visibility_pending", Retryable: true}
	}
	close(round.done)
	return round.err
}
