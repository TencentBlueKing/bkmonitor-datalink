// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

// --- 响应体

// CheckQueryTsDataResponse check 接口成功响应体。
type CheckQueryTsDataResponse struct {
	// Data 每项为子查询对应 tsdb.Instance.GetRequestBody(ctx) 的序列化结果。直查 VM 常为单元素 VmQueryCheckBody：metricql 由 vmCheckMetricql 生成后经 metadata.SetCheckPreviewMetricQL 写入，VM 实例在 GetRequestBody 中与 GetExpand 一并读出。非直查若某存储无预览体则跳过该项；若最终无任何元素则 400。不下发真实 TSDB。
	Data []any `json:"data"`
	// TraceID 链路 ID（与 trace span 一致）。
	TraceID string `json:"trace_id"`
}

// --- HTTP Handlers

// HandlerCheckQueryTs
// @Summary	query ts monitor check by ts
// @ID		check_query_ts
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  CheckQueryTsDataResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /check/query/ts [post]
func HandlerCheckQueryTs(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{c: c}
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "check-query-ts")
	defer span.End(&err)

	queryTs := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(queryTs)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryTs,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	user := metadata.GetUser(ctx)
	if user.SpaceUID != "" {
		queryTs.SpaceUid = user.SpaceUID
	}

	data, err := checkQueryTsData(ctx, queryTs)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, CheckQueryTsDataResponse{
		Data:    data,
		TraceID: span.TraceID(),
	})
}

// HandlerCheckQueryPromQL
// @Summary	query promql monitor check by ts
// @ID		check_query_promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryPromQL  		true   "json data"
// @Success  200                   	{object}  CheckQueryTsDataResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /check/query/ts/promql [post]
func HandlerCheckQueryPromQL(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{c: c}
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "check-query-promql")
	defer span.End(&err)

	queryPromQL := &structured.QueryPromQL{}
	err = json.NewDecoder(c.Request.Body).Decode(queryPromQL)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgParserPromQL,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	queryTs, err := promQLToStruct(ctx, queryPromQL)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	user := metadata.GetUser(ctx)
	if user.SpaceUID != "" {
		queryTs.SpaceUid = user.SpaceUID
	}

	data, err := checkQueryTsData(ctx, queryTs)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, CheckQueryTsDataResponse{
		Data:    data,
		TraceID: span.TraceID(),
	})
}

// --- 编排：QueryReference → 直查 / 非直查预览

// checkQueryTsData 将 QueryTs 转为 QueryReference，再按直查/非直查组装预览（不调真实 TSDB）。
// 直查 VM：vmCheckMetricql 后 SetCheckPreviewMetricQL；与 queryTsToInstanceAndStmt 同源 GetTsDbInstance(VM)，Instance.GetRequestBody(ctx) 产出 VmQueryCheckBody。
// 非直查：统一经 GetTsDbInstance + GetRequestBody，默认实现返回 nil 预览体则跳过。todo: 未来扩展具体预览体
func checkQueryTsData(ctx context.Context, q *structured.QueryTs) ([]any, error) {
	qr, err := checkQueryTsToReference(ctx, q)
	if err != nil {
		return nil, err
	}

	if metadata.GetQueryParams(ctx).IsDirectQuery() {
		// 与 queryTsToInstanceAndStmt 直查分支一致：ToVmExpand + SetExpand，后续与 DirectQuery 同源读 GetExpand。
		vmExpand := query.ToVmExpand(ctx, qr)
		metadata.SetExpand(ctx, vmExpand)

		promQL, err := vmCheckMetricql(ctx, q, qr)
		if err != nil {
			return nil, err
		}
		metadata.SetCheckPreviewMetricQL(ctx, promQL)
		instance := prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageID:   metadata.VictoriaMetricsStorageType,
			StorageType: metadata.VictoriaMetricsStorageType,
		})
		if instance == nil {
			return nil, fmt.Errorf("instance is null for direct vm check")
		}
		item, err := getCheckPreview(ctx, instance)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return nil, fmt.Errorf("empty check preview for direct vm check")
		}
		return []any{item}, nil
	}

	// 非直查：遍历子查询 GetTsDbInstance + getCheckPreview（GetRequestBody 默认 nil 预览体则跳过）todo: 未来扩展各存储预览体
	var out []any
	var rangeErr error
	qr.Range("", func(qry *metadata.Query) {
		if rangeErr != nil {
			return
		}
		instance := prometheus.GetTsDbInstance(ctx, qry)
		if instance == nil {
			rangeErr = fmt.Errorf("instance is null, with storageID %s", qry.StorageID)
			return
		}
		var item any
		item, rangeErr = getCheckPreview(ctx, instance)
		if rangeErr != nil {
			return
		}
		if item != nil {
			out = append(out, item)
		}
	})
	if rangeErr != nil {
		return nil, rangeErr
	}
	if len(out) == 0 {
		return nil, metadata.NewMessage(
			metadata.MsgQueryReference,
			"未解析到可路由的查询",
		).Error(ctx, fmt.Errorf("empty check query reference"))
	}
	return out, nil
}

// --- QueryReference（与 queryTsToInstanceAndStmt 前置对齐）

// checkQueryTsToReference 在 ToQueryReference 前的处理与 queryTsToInstanceAndStmt 内联逻辑一致（复制，避免改动 query.go）。
func checkQueryTsToReference(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
	var err error
	if DefaultQueryListLimit > 0 {
		if len(q.QueryList) > DefaultQueryListLimit {
			err = fmt.Errorf("the number of query lists cannot be greater than %d", DefaultQueryListLimit)
		}
	}
	for _, ql := range q.QueryList {
		ql.NotPromFunc = false
		ql.AlignInfluxdbResult = AlignInfluxdbResult && !q.Reference && !q.NotTimeAlign
		ql.OrderBy = q.OrderBy
		if ql.Step == "" {
			ql.Step = q.Step
		}
		if ql.Limit == 0 && q.Limit > 0 {
			ql.Limit = q.Limit
		}
		if ql.From == 0 && q.From > 0 {
			ql.From = q.From
		}
	}
	if q.LookBackDelta != "" {
		if _, e := time.ParseDuration(q.LookBackDelta); e != nil {
			return nil, e
		}
	}
	if q.Step == "" {
		q.Step = promql.GetDefaultStep().String()
	}
	qr, err2 := q.ToQueryReference(ctx)
	if err2 != nil {
		return nil, err2
	}
	_ = err
	return qr, nil
}

// --- 直查 VM：VmExpand、MetricQL 预览、VmQueryCheckBody

// vmExpandForCheck 优先 metadata.GetExpand(ctx)（直查 Check 在 checkQueryTsData 内已 SetExpand，与 queryTsToInstanceAndStmt 一致）；
// ctx 未写入时退回 ToVmExpand(qr)，便于单测直接调用 vmCheckMetricql。
func vmExpandForCheck(ctx context.Context, qr metadata.QueryReference) *metadata.VmExpand {
	if v := metadata.GetExpand(ctx); v != nil {
		return v
	}
	return query.ToVmExpand(ctx, qr)
}

// vmCheckMetricql 直查 VM 的 MetricQL 预览（内存拼装）；q 与 qr 须同源，一般 qr 来自 q.ToQueryReference。
func vmCheckMetricql(ctx context.Context, q *structured.QueryTs, qr metadata.QueryReference) (string, error) {
	// 过滤串与真实 VM 侧 metric_filter_condition 同源；无展开或无条件则无法预览。
	vmExpand := vmExpandForCheck(ctx, qr)
	if vmExpand == nil || len(vmExpand.MetricFilterCondition) == 0 {
		return "", metadata.NewMessage(
			metadata.MsgQueryReference,
			"vm 展开或 metric_filter_condition 为空",
		).Error(ctx, fmt.Errorf("vm expand metric filter empty"))
	}

	// 空 PromExprOption 时 ToPromExpr 不会校验 Conditions；此处与 ToPromQL 路径对齐，避免无法 ToProm 的条件仍生成预览。
	for _, ql := range q.QueryList {
		if _, _, err := ql.Conditions.ToProm(); err != nil {
			return "", err
		}
	}

	// 不填 ReferenceNameMetric / LabelMatcher，表达式叶子仍为 reference 名（a、b），便于下一步文本替换
	promExprOpt := &structured.PromExprOption{}
	expr, err := q.ToPromExpr(ctx, promExprOpt)
	if err != nil {
		return "", err
	}
	out := expr.String()

	// 按词边界把 ref 换成 {MetricFilterCondition[ref]}；ref 名按长度降序，避免 a 误替换 ab
	refs := make([]string, 0, len(vmExpand.MetricFilterCondition))
	for ref := range vmExpand.MetricFilterCondition {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return len(refs[i]) > len(refs[j]) })

	for _, ref := range refs {
		filter := vmExpand.MetricFilterCondition[ref]
		if filter == "" {
			continue
		}
		// QuoteMeta：ref 中的正则元字符按字面匹配；\b：整词替换，避免误伤更长标识符
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(ref) + `\b`)
		out = re.ReplaceAllString(out, "{"+filter+"}")
	}
	return out, nil
}

// --- 工具

// getCheckPreview 获取 instance 的预览体；instance 为 nil 或预览体为 nil 时返回 nil 并打 Warn。
func getCheckPreview(ctx context.Context, instance tsdb.Instance) (any, error) {
	if instance == nil {
		log.Warnf(ctx, "check: skip nil tsdb.Instance preview")
		return nil, nil
	}
	item, err := instance.GetRequestBody(ctx)
	if err != nil {
		return nil, err
	}
	if item == nil {
		log.Warnf(ctx, "check: skip nil preview body for instance type %q", instance.InstanceType())
		return nil, nil
	}
	return item, nil
}
