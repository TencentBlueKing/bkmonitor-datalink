// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// The environment diagnosis over the channel: the strategy pages come from
// the fleet's /api/diagnose, and the first page adds the deployment as the
// operations that already read it see it -- the fleet's health, alarmd's own
// Pods, and the Redis servers' INFO. Nothing here reads anything those do not
// read; a part that could not be read is named in the deployment section, not
// left out of it.

// DiagnoseDefaultRows is a page's rows when the caller does not say: the
// row bound, so the page's byte budget decides where it is cut. Deciding
// 2,000 rows against a built view takes about 19 ms on the design's large
// deployment (BenchmarkDiagnosisPage); the first page's cost is reading the
// fleet's snapshots, which later pages of the same diagnosis reuse.
const DiagnoseDefaultRows = 2000

// healthKeys is what the deployment section keeps of the fleet's health: the
// facts a reader needs to place the strategy rows, not the whole response.
var healthKeys = []string{"health", "expected", "covered", "determined", "unknown", "replicas_not_ready",
	"activation", "activation_replica", "view_stream", "assignment_scope", "rebalance", "degradations",
	"dependencies", "dependencies_replicas", "source_standing", "gaps", "linkd_console", "workers", "builds",
	"leader_round", "leader_round_replica"}

// DeploymentFinding is one named fact about the deployment the diagnosis
// found, from a closed list.
type DeploymentFinding struct {
	Code   string `json:"code"`
	Scope  string `json:"scope"`
	Detail string `json:"detail"`
}

// The deployment findings, closed.
const (
	FindingRedisEvictionPolicy  = "REDIS_EVICTION_POLICY_NOT_NOEVICTION"
	FindingRedisEvictedKeys     = "REDIS_EVICTED_KEYS"
	FindingRedisInfoUnknown     = "REDIS_INFO_UNKNOWN"
	FindingRedisMemoryNearLimit = "REDIS_MEMORY_NEAR_LIMIT"
	FindingPartUnreadable       = "DEPLOYMENT_PART_UNREADABLE"
)

// RedisMemoryNearLimitPercent is where used_memory over maxmemory becomes a
// finding: past it, not at it. A report, never a gate: nothing refuses on it.
const RedisMemoryNearLimitPercent = 80

// RedisMemory is one server's memory standing as the diagnosis reads it:
// Share is the used share of maxmemory to two decimals, "不限" when maxmemory
// is 0, or "未知" when either count was not reported.
type RedisMemory struct {
	Scope      string `json:"scope"`
	UsedMemory string `json:"used_memory,omitempty"`
	MaxMemory  string `json:"maxmemory,omitempty"`
	Share      string `json:"share"`
}

// DiagnoseOperation composes the diagnosis from the native fleet handler and
// the operations already registered for the deployment: k8s.pods and
// store.info are looked up by id and run in process when present.
func DiagnoseOperation(native http.Handler, existing []Operation) Operation {
	byID := map[string]Operation{}
	for _, op := range existing {
		byID[op.ID] = op
	}
	cursor := Field{Type: "string", MinLength: 1, MaxLength: 512, Pattern: "^[A-Za-z0-9_-]+$",
		Description: "上一页返回的 next_cursor，原样传回；首页省略。", Source: "diagnose.environment next_cursor"}
	var minRows, maxRows int64 = 1, 2000
	return Operation{
		ID:            "diagnose.environment",
		Summary:       "对环境做一次全量诊断：以策略缓存的活动集为全集，每条策略一行结论，首页附部署层；按页自证覆盖，跨页汇总由 CLI 核对。",
		EvidenceScope: "deployment",
		Fields: map[string]Field{
			"cursor": cursor,
			"limit":  {Type: "integer", Minimum: &minRows, Maximum: &maxRows, Description: "本页最多行数；默认 2000，实际按 1 MiB 字节上限截断。"},
		},
		Limits:       map[string]any{"rows_default": DiagnoseDefaultRows, "rows_max": maxRows, "page_bytes": 1 << 20},
		OutputSchema: map[string]any{"type": "object"},
		Run: func(ctx context.Context, p Params) Outcome {
			query := url.Values{"limit": {strconv.Itoa(p.Int("limit", DiagnoseDefaultRows))}}
			first := p.String("cursor") == ""
			if !first {
				query.Set("cursor", p.String("cursor"))
			}
			out := invokeNative(ctx, native, "/api/diagnose", query)
			if out.Error != nil {
				return out
			}
			page, _ := out.Value.(map[string]any)
			if errText, ok := page["error"].(string); ok {
				return Outcome{Error: &Failure{Code: "diagnosis_refused", Message: errText}}
			}
			out.Complete = diagnosisPageHolds(page)
			if !out.Complete {
				out.Limitations = append(out.Limitations, "This page does not prove its coverage; see page.holds and universe.status.")
			}
			if first {
				page["deployment"] = deploymentSection(ctx, native, byID)
			}
			if next, _ := page["next_cursor"].(string); next != "" {
				out.Next = append(out.Next, Call{Operation: "diagnose.environment", Params: Params{"cursor": next, "limit": p.Int("limit", DiagnoseDefaultRows)},
					Reason: "读取下一页；各页汇总与覆盖等式由 CLI 核对。"})
			}
			out.Summary = diagnosisPageSummary(page)
			out.Value = page
			return out
		},
	}
}

func diagnosisPageHolds(page map[string]any) bool {
	universe, _ := page["universe"].(map[string]any)
	status, _ := universe["status"].(string)
	body, _ := page["page"].(map[string]any)
	holds, _ := body["holds"].(bool)
	return status == "ok" && holds
}

func diagnosisPageSummary(page map[string]any) string {
	universe, _ := page["universe"].(map[string]any)
	body, _ := page["page"].(map[string]any)
	if status, _ := universe["status"].(string); status != "ok" {
		reason, _ := universe["reason"].(string)
		return "全集读不到（" + reason + "），本次诊断无效"
	}
	return fmt.Sprintf("全集 %v 条；本页应覆盖 %v 条、写出 %v 条", universe["count"], body["ids_expected"], body["rows"])
}

// deploymentSection reads the deployment through the existing reads. Every
// part is present: read, or unreadable with the reason.
func deploymentSection(ctx context.Context, native http.Handler, ops map[string]Operation) map[string]any {
	section := map[string]any{}
	findings := []DeploymentFinding{}
	health := invokeNative(ctx, native, "/api/health", url.Values{})
	if health.Error != nil {
		section["fleet"] = map[string]any{"status": "unreadable", "reason": health.Error.Code}
		findings = append(findings, DeploymentFinding{Code: FindingPartUnreadable, Scope: "fleet", Detail: health.Error.Code})
	} else if value, ok := health.Value.(map[string]any); ok {
		kept := map[string]any{}
		for _, key := range healthKeys {
			if v, found := value[key]; found {
				kept[key] = v
			}
		}
		section["fleet"] = kept
	}
	section["replicas"] = runPart(ctx, ops, "k8s.pods", &findings)
	info := runPart(ctx, ops, "store.info", &findings)
	section["store_info"] = info
	redis, memory := redisFindings(info)
	findings = append(findings, redis...)
	if memory != nil {
		section["redis_memory"] = memory
	}
	section["findings"] = findings
	return section
}

// runPart runs an existing operation in process; an absent or failing one is
// a named unreadable part and a finding, never a missing key.
func runPart(ctx context.Context, ops map[string]Operation, id string, findings *[]DeploymentFinding) any {
	op, ok := ops[id]
	if !ok || op.Run == nil {
		*findings = append(*findings, DeploymentFinding{Code: FindingPartUnreadable, Scope: id, Detail: "operation not registered"})
		return map[string]any{"status": "unreadable", "reason": "not_registered"}
	}
	if op.Availability != nil {
		if availability := op.Availability(); !availability.Available {
			*findings = append(*findings, DeploymentFinding{Code: FindingPartUnreadable, Scope: id, Detail: availability.Reason})
			return map[string]any{"status": "unreadable", "reason": availability.Reason}
		}
	}
	result := op.Run(ctx, Params{})
	if result.Error != nil {
		*findings = append(*findings, DeploymentFinding{Code: FindingPartUnreadable, Scope: id, Detail: result.Error.Code})
		return map[string]any{"status": "unreadable", "reason": result.Error.Code, "message": result.Error.Message}
	}
	return result.Value
}

// redisFindings is the named findings on every Redis server's INFO: an
// eviction policy other than noeviction, keys already evicted, and memory
// past RedisMemoryNearLimitPercent of maxmemory. A server whose INFO did not
// answer, or answered without the field, is its own finding: an unread
// policy is not a safe one, and an unread memory count is not room to
// spare. Beside them, every answering server's memory standing, so a server
// without a limit reads as one ("不限") rather than as a server nobody
// checked.
func redisFindings(info any) ([]DeploymentFinding, []RedisMemory) {
	encoded, err := json.Marshal(info)
	if err != nil {
		return nil, nil
	}
	var result struct {
		Servers []struct {
			Roles   []string          `json:"roles"`
			Address string            `json:"address"`
			Status  string            `json:"status"`
			Fields  map[string]string `json:"fields"`
		} `json:"servers"`
	}
	if json.Unmarshal(encoded, &result) != nil || result.Servers == nil {
		return nil, nil
	}
	var findings []DeploymentFinding
	memory := []RedisMemory{}
	for _, server := range result.Servers {
		scope := fmt.Sprintf("%v@%s", server.Roles, server.Address)
		if server.Status != "ok" {
			findings = append(findings, DeploymentFinding{Code: FindingRedisInfoUnknown, Scope: scope, Detail: "INFO " + server.Status})
			continue
		}
		policy, known := server.Fields["maxmemory_policy"]
		switch {
		case !known:
			findings = append(findings, DeploymentFinding{Code: FindingRedisInfoUnknown, Scope: scope, Detail: "maxmemory_policy not reported"})
		case policy != "noeviction":
			findings = append(findings, DeploymentFinding{Code: FindingRedisEvictionPolicy, Scope: scope, Detail: "maxmemory_policy=" + policy})
		}
		if evicted, known := server.Fields["evicted_keys"]; !known {
			findings = append(findings, DeploymentFinding{Code: FindingRedisInfoUnknown, Scope: scope, Detail: "evicted_keys not reported"})
		} else if n, err := strconv.ParseInt(evicted, 10, 64); err != nil || n > 0 {
			findings = append(findings, DeploymentFinding{Code: FindingRedisEvictedKeys, Scope: scope, Detail: "evicted_keys=" + evicted})
		}
		standing, finding := redisMemoryOf(scope, server.Fields)
		memory = append(memory, standing)
		if finding != nil {
			findings = append(findings, *finding)
		}
	}
	return findings, memory
}

// redisMemoryOf reads one server's used_memory against its maxmemory. The
// comparison is in integers -- used * 100 against maxmemory * percent -- so
// the line sits exactly at the percent with no rounding on either side.
func redisMemoryOf(scope string, fields map[string]string) (RedisMemory, *DeploymentFinding) {
	standing := RedisMemory{Scope: scope, UsedMemory: fields["used_memory"], MaxMemory: fields["maxmemory"], Share: "未知"}
	used, usedErr := strconv.ParseUint(fields["used_memory"], 10, 64)
	limit, limitErr := strconv.ParseUint(fields["maxmemory"], 10, 64)
	switch {
	case limitErr != nil:
		return standing, &DeploymentFinding{Code: FindingRedisInfoUnknown, Scope: scope, Detail: "maxmemory not reported"}
	case limit == 0:
		// No limit: nothing to be near. Said on the standing, not as a finding.
		standing.Share = "不限"
		return standing, nil
	case usedErr != nil:
		return standing, &DeploymentFinding{Code: FindingRedisInfoUnknown, Scope: scope, Detail: "used_memory not reported"}
	}
	// Two decimals, so a share just past the line does not print as the line
	// itself: 8001 of 10000 is 80.01%, not 80.0%.
	standing.Share = strconv.FormatFloat(float64(used)*100/float64(limit), 'f', 2, 64) + "%"
	if used > limit || used*100 > limit*RedisMemoryNearLimitPercent {
		return standing, &DeploymentFinding{Code: FindingRedisMemoryNearLimit, Scope: scope,
			Detail: fmt.Sprintf("used_memory=%d maxmemory=%d share=%s", used, limit, standing.Share)}
	}
	return standing, nil
}
