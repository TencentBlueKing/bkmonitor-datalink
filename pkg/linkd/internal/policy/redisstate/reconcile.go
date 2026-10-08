// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// DeleteSuppressionWindow 只移除完整观察到的 owner/代次，调用方须已在 owner 的 fingerprint lease 内复核真实资格。
// 返回 false 表示已消失或代次改变，不证明本次已删除；不修改 Event 首次裁决缓存。
func (s *Store) DeleteSuppressionWindow(ctx context.Context, w SuppressionWindow) (bool, error) {
	if w.Validate() != nil {
		return false, ErrState
	}
	if w.Kind == "aggregation" {
		return s.ReleaseAggregation(ctx, w.TenantID, AggregationDecision{Role: "owner", WindowID: w.ID, Epoch: w.Epoch, OwnerAlertID: w.OwnerAlertID, OwnerEventID: w.OwnerEventID, OwnerSourceID: w.OwnerSourceID, OwnerFingerprint: w.OwnerFingerprint, StartedAtMillis: w.StartedAtMillis, ExpiresAtMillis: w.ExpiresAtMillis})
	}
	// 未绑定计数是合法的新告警候选，不提供绕过防抖的强制清空入口。
	if w.OwnerAlertID == "" {
		return false, ErrState
	}
	parts := strings.Split(w.ID, ":")
	base := s.base(w.TenantID)
	counter := base + ":clip:" + w.ID
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := deleteClipWindowScript.Run(ctx, s.client, []string{counter, counter + ":meta", base + ":clip-index:" + parts[0], s.suppressionIndex(w.TenantID, "clip")}, parts[0], w.Epoch, w.OwnerAlertID, w.ID).Int()
	if err != nil {
		return false, err
	}
	if n < 0 {
		return false, ErrState
	}
	return n == 1, nil
}

var deleteClipWindowScript = redis.NewScript(`
local mt=redis.call('TYPE',KEYS[2]).ok;if mt=='none' then return 0 end;if mt~='hash' then return -1 end
if redis.call('HGET',KEYS[2],'scope')~=ARGV[1] then return -1 end
if redis.call('HGET',KEYS[2],'epoch')~=ARGV[2] or redis.call('HGET',KEYS[2],'owner')~=ARGV[3] then return 0 end
local ct=redis.call('TYPE',KEYS[1]).ok;if ct~='none' and ct~='zset' then return -1 end
local it=redis.call('TYPE',KEYS[3]).ok;if it~='none' and it~='set' then return -1 end
local rt=redis.call('TYPE',KEYS[4]).ok;if rt~='none' and rt~='zset' then return -1 end
redis.call('DEL',KEYS[1],KEYS[2]);redis.call('SREM',KEYS[3],KEYS[1]);redis.call('ZREM',KEYS[4],ARGV[4])
if redis.call('SCARD',KEYS[3])==0 then redis.call('DEL',KEYS[3]) end
return 1
`)
