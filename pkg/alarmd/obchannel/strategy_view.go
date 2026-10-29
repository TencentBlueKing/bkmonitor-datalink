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
	"net/http"
	"net/url"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// strategyViewOperation is the admin page's strategy drawer: the strategy's
// row and objects from the diagnosis's board computation, and its frozen
// configuration from one catalog read (include=config: each stored object
// of its Plans once, within the read's timeout), joined and
// written as words. A front read, charged to the front budget by its time.
func strategyViewOperation(handler http.Handler, limits map[string]any) Operation {
	return Operation{ID: "strategy.view", EvidenceScope: "deployment",
		Summary: "策略详情（管理页抽屉）：结论（同看板一行）、应检测（周期、数据源类型、各级别算法、无数据、目标范围、生效时段，服务端写成人话，不给原始配置）、" +
			"实际检测（是否按时、检测对象按是否按时的计数、未恢复告警数）、检查码与知识库锚点。结论与实际检测与看板同一次计算（30 秒内共用）；" +
			"应检测是该策略冻结配置的一次有界读取，读不到时写明原因（known=false），不是“什么都不检测”。不含查询文本、查询组、副本、修订号。",
		Fields:   map[string]Field{"strategy_id": {Type: "string", Description: "外部策略ID。", Source: "用户给定或 strategy.board 的行", MinLength: 1, MaxLength: 256}},
		Required: []string{"strategy_id"}, OutputSchema: SchemaOf(fleet.StrategyView{}), Limits: limits,
		Run: func(ctx context.Context, p Params) Outcome {
			id := p.String("strategy_id")
			read := invokeNative(ctx, handler, "/api/diagnose", url.Values{"view": {"1"}, "strategy_id": {id}})
			result, _ := read.Value.(map[string]any)
			if code, _ := result["error"].(string); code != "" {
				if code == "STRATEGY_NOT_IN_UNIVERSE" {
					return Outcome{Error: &Failure{Code: "not_found", Message: "策略不在检测全集里：" + id}}
				}
				reason, _ := result["reason"].(string)
				return Outcome{Error: &Failure{Code: "diagnosis_refused", Message: strings.TrimSpace(code + " " + reason)}}
			}
			if read.Error != nil {
				return read
			}
			var actual fleet.StrategyViewActual
			if !remarshal(result, &actual) {
				return Outcome{Error: &Failure{Code: "invalid_upstream_response", Message: "The diagnosis view did not decode."}}
			}
			out := Outcome{Complete: true}
			var standing *fleet.StrategyStanding
			config := invokeNative(ctx, handler, "/api/strategies/"+url.PathEscape(id), url.Values{"include": {fleet.IncludeConfig}})
			if body, ok := config.Value.(map[string]any); ok && config.Error == nil {
				var decoded fleet.StrategyStanding
				if remarshal(body, &decoded) {
					standing = &decoded
				}
			}
			view := fleet.ComposeStrategyView(actual, standing)
			if !view.Expected.Known && view.Expected.UnknownWord != fleet.ExpectedNotAccepted {
				out.Complete = false
				out.Limitations = append(out.Limitations, "应检测未读到："+view.Expected.UnknownWord)
			}
			var value map[string]any
			if !remarshal(view, &value) {
				return Outcome{Error: &Failure{Code: "internal", Message: "The strategy view did not encode."}}
			}
			out.Value, out.Summary = value, view.Conclusion.Line
			return out
		}}
}

// remarshal moves a decoded JSON value into another shape through its bytes.
func remarshal(from, to any) bool {
	raw, err := json.Marshal(from)
	return err == nil && json.Unmarshal(raw, to) == nil
}
