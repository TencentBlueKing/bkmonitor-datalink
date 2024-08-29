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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

type CheckItem struct {
	Error    error  `json:"error,omitempty"`
	StepName string `json:"step_name,omitempty"`
	JsonData string `json:"json_data,omitempty"`
}

func (c *CheckItem) String() string {
	var s []string
	s = append(s, fmt.Sprintf("step-name: %s", c.StepName))
	if c.Error != nil {
		s = append(s, fmt.Sprintf("error: %v", c.Error))
	} else {
		s = append(s, fmt.Sprintf("data: %s", c.JsonData))
	}
	return strings.Join(s, "\n")
}

type CheckResponse struct {
	List []*CheckItem `json:"list"`
}

func (c *CheckResponse) Step(name string, data interface{}) {
	var jsonData string
	s, err := json.Marshal(data)
	if err != nil {
		jsonData = fmt.Sprintf("%+v", data)
	} else {
		jsonData = fmt.Sprintf("%s", s)
	}

	c.List = append(c.List, &CheckItem{
		StepName: name,
		JsonData: jsonData,
	})
}

func (c *CheckResponse) Error(name string, err error) {
	c.List = append(c.List, &CheckItem{
		StepName: name,
		Error:    err,
	})
}

func (c *CheckResponse) String() string {
	var s []string
	for _, i := range c.List {
		s = append(s, i.String())
	}
	return strings.Join(s, "\n-------------------------------------------------\n")
}

// HandlerCheckQueryTs
// @Summary	query ts monitor check by ts
// @ID		check_query_ts
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  CheckResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /check/query/ts [post]
func HandlerCheckQueryTs(c *gin.Context) {
	var (
		ctx           = c.Request.Context()
		checkResponse = &CheckResponse{}
	)

	// 解析请求 body
	query := &structured.QueryTs{}
	err := json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		checkResponse.Error("query ts json decoder", err)
		return
	}

	checkQueryTs(ctx, query, checkResponse)
	c.String(http.StatusOK, checkResponse.String())
}

// HandlerCheckQueryPromQL
// @Summary	query promql monitor check by ts
// @ID		check_query_promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryPromQL  		true   "json data"
// @Success  200                   	{object}  CheckResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /check/query/ts [post]
func HandlerCheckQueryPromQL(c *gin.Context) {
	var (
		ctx           = c.Request.Context()
		checkResponse = &CheckResponse{}
	)

	// 解析请求 body
	queryPromQL := &structured.QueryPromQL{}
	err := json.NewDecoder(c.Request.Body).Decode(queryPromQL)
	if err != nil {
		checkResponse.Error("query promQL json decoder", err)
		return
	}

	// promql to struct
	query, err := promQLToStruct(ctx, queryPromQL)
	if err != nil {
		checkResponse.Error("promQLToString", err)
		return
	}

	checkQueryTs(ctx, query, checkResponse)
	c.String(http.StatusOK, checkResponse.String())
}

// checkQueryTs 根据传入的查询进行校验判断
func checkQueryTs(ctx context.Context, q *structured.QueryTs, r *CheckResponse) {
	var err error

	r.Step("query ts", q)

	user := metadata.GetUser(ctx)
	r.Step("metadata user", user)

	// 查询转换信息
	qr, err := q.ToQueryReference(ctx)
	if err != nil {
		r.Error("q.ToQueryReference", err)
		return
	}
	r.Step("query-reference", qr)

	start, end, _, _, err := structured.ToTime(q.Start, q.End, q.Step, q.Timezone)
	if err != nil {
		r.Error("structured.ToTime", err)
		return
	}

	// 写入查询缓存
	metadata.GetQueryParams(ctx).SetTime(start.Unix(), end.Unix())

	// 判断是否查询 vm
	ok, vmExpand, err := qr.CheckVmQuery(ctx)
	if err != nil {
		r.Error("qr.CheckVmQuery", err)
		return
	}

	promQL, err := q.ToPromQL(ctx)
	if err != nil {
		r.Error("q.ToPromQL", err)
		return
	}
	r.Step("query promQL", promQL)

	// vm query
	if ok {
		r.Step("query instance", consul.VictoriaMetricsStorageType)
		r.Step("query vmExpand", vmExpand)
	} else {
		for _, qm := range qr {
			for _, qry := range qm.QueryList {
				instance := prometheus.GetTsDbInstance(ctx, qry)
				if instance == nil {
					r.Error("prometheus.GetInstance", fmt.Errorf("instance is null, with storageID %s", qry.StorageID))
					continue
				}

				r.Step("instance id", qry.StorageID)
				r.Step("instance type", instance.GetInstanceType())
				r.Step("query struct", qry)
			}
		}
	}

	status := metadata.GetStatus(ctx)
	if status != nil {
		r.Step("metadata status", status)
	}
}
