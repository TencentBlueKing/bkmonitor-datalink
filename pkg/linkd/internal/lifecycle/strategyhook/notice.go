// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategyhook

// 脚本内判定实际变化，避免多个 Worker 在独立 SISMEMBER 与写入之间竞争而重复通知。
// PUBLISH 返回 0 仅表示没有订阅者。脚本原子执行不代表错误回滚：发布失败时集合可能已改变。
const changeScript = `
local changed = redis.call(ARGV[1], KEYS[1], ARGV[2])
if changed > 0 then
  redis.call('PUBLISH', ARGV[3], ARGV[4])
end
return changed
`

// changeNotice 是 v1 失效通知，不包含成员列表或告警内容；订阅者根据 key 重新读取集合。
type changeNotice struct {
	Version    int    `json:"version"`
	BKTenantID string `json:"bk_tenant_id"`
	StrategyID string `json:"strategy_id"`
	Key        string `json:"key"`
	Database   int    `json:"database"`
}
