// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"slices"

	"github.com/redis/go-redis/v9"
)

// TaskWriter 把当前任务授权与业务 Lua 写入放在一次 Redis 调用中。
// 只读取现有调度快照，不创建锁或授权副本；续租由原控制面负责。
type TaskWriter struct {
	client   redis.UniversalClient
	stateKey string
	assigned Task
}

// NewTaskWriter 绑定 Agent 已获准启动的任务代次；task 不得来自业务消息。
// 不冻结初始 Expires，后续每次写入读取控制面已续租的当前授权。
func NewTaskWriter(client redis.UniversalClient, deployment string, task Task) *TaskWriter {
	return &TaskWriter{client: client, stateKey: "linkd:dispatch:{" + deployment + "}:state", assigned: task}
}

// Eval 仅在当前任务仍属于本 worker/代次、处于 starting/running 且未过期时执行脚本。
// 调度快照的格式和权限解释归 taskdispatch 所有，业务存储不解析私有协调状态。
func (w *TaskWriter) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	keys = append(slices.Clone(keys), w.stateKey)
	args = append(slices.Clone(args), w.assigned.ID, w.assigned.Worker, w.assigned.Epoch, w.assigned.Source, w.assigned.Role)
	return w.client.Eval(ctx, taskExpiryScript+taskWriteGuard+script, keys, args...)
}

// Go time.Time 以 RFC3339Nano 存在协调快照中。解析其时区和小数部分，按 Redis
// 服务端时钟判断授权，不依赖 worker 本机时钟。毫秒截断最多提前不足一毫秒拒绝。
const taskExpiryScript = `
local function expires_millis(value)
 local y,m,d,h,mi,s = string.match(value, '^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)')
 if not y then return nil end
 y,m,d,h,mi,s=tonumber(y),tonumber(m),tonumber(d),tonumber(h),tonumber(mi),tonumber(s)
 local offset=0
 if string.sub(value,-1) ~= 'Z' then
  local sign,oh,om=string.match(value,'([+-])(%d%d):(%d%d)$')
  if not sign then return nil end
  offset=(tonumber(oh)*60+tonumber(om))*60
  if sign=='-' then offset=-offset end
 end
 local fraction=string.match(value,'%.(%d+)') or ''
 local millis=tonumber(string.sub(fraction..'000',1,3))
 -- 公历以三月为年首计算整天数，再转换到 Unix epoch。
 if m<=2 then y=y-1 end
 local era=math.floor(y/400)
 local yoe=y-era*400
 local shifted=m+9
 if m>2 then shifted=m-3 end
 local doy=math.floor((153*shifted+2)/5)+d-1
 local doe=yoe*365+math.floor(yoe/4)-math.floor(yoe/100)+doy
 local days=era*146097+doe-719468
 return ((days*24+h)*3600+mi*60+s-offset)*1000+millis
end
`

const taskWriteGuard = `
local raw=redis.call('GET',KEYS[#KEYS])
if not raw then return redis.error_reply('TASK_AUTHORITY_LOST') end
local state=cjson.decode(raw)
local task=state.tasks[ARGV[#ARGV-4]]
if not task or task.worker~=ARGV[#ARGV-3] or task.epoch~=tonumber(ARGV[#ARGV-2])
 or task.source~=ARGV[#ARGV-1] or task.role~=ARGV[#ARGV]
 or (task.phase~='starting' and task.phase~='running') then
 return redis.error_reply('TASK_AUTHORITY_LOST')
end
local expires=expires_millis(task.expires)
local now=redis.call('TIME')
if not expires or expires<=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000) then
 return redis.error_reply('TASK_AUTHORITY_LOST')
end
`
