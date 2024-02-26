// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RealTimeNode 实时计算节点
type RealTimeNode struct {
	DefaultAggMethod string
	SourceRtId       string
	BkBizId          int
	ProcessRtId      string
	processRtId      string
	AggInterval      int
	Sql              string
	NamePrefix       string
	OutputRtId       string
	BaseNode
}

func NewRealTimeNode(sourceRtId string, aggInterval int, aggMethod string, metricFields, dimensionFields []string, sql, namePrefix, outputRtId string, parentList []Node) *RealTimeNode {
	n := &RealTimeNode{BaseNode: *NewBaseNode(parentList)}
	n.NodeType = "realtime"
	if aggMethod != "" {
		n.DefaultAggMethod = aggMethod
	} else {
		n.DefaultAggMethod = "MAX"
	}
	n.SourceRtId = sourceRtId
	splitStr := strings.SplitN(sourceRtId, "_", 2)
	if len(splitStr) != 2 {
		return nil
	} else {
		bizStr := splitStr[0]
		bizId, err := strconv.Atoi(bizStr)
		if err != nil {
			logger.Errorf("parse bkBizId from sourceRtId [%s] failed", sourceRtId)
			return nil
		}
		n.BkBizId = bizId
		n.ProcessRtId = splitStr[1]
	}
	n.AggInterval = aggInterval
	if sql != "" {
		n.Sql = sql
	} else {
		if aggInterval != 0 && (aggMethod != "" || len(metricFields) != 0 || len(dimensionFields) != 0) {
			logger.Errorf("please provide 'agg_method', 'metric_fields', 'dimension_fields', if 'sql' does not exist")
			return nil
		}
		tempSql := n.GenStatisticSql(n.SourceRtId, aggMethod, metricFields, dimensionFields)
		n.Sql = strings.TrimSpace(tempSql)
	}
	n.NamePrefix = namePrefix
	n.OutputRtId = outputRtId
	// 指定输出表名
	strList := strings.SplitN(n.OutputRtId, "_", 2)
	if len(strList) != 2 {
		n.processRtId = ""
	} else {
		n.processRtId = strList[1]
	}
	return n
}

func (n RealTimeNode) Equal(other map[string]interface{}) bool {
	c := n.Config()
	if equal, _ := jsonx.CompareObjects(c["from_result_table_ids"], other["from_result_table_ids"]); equal {
		if equal, _ := jsonx.CompareObjects(c["table_name"], other["table_name"]); equal {
			if equal, _ := jsonx.CompareObjects(c["bk_biz_id"], other["bk_biz_id"]); equal {
				return true
			}
		}
	}
	return false
}

// TableName 输出表名（不带业务ID前缀）
func (n RealTimeNode) TableName() string {
	if n.processRtId != "" {
		return n.processRtId
	}
	return n.ProcessRtId
}

// OutputTableName 输出表名（带上业务ID前缀）
func (n RealTimeNode) OutputTableName() string {
	return fmt.Sprintf("%d_%s", n.BkBizId, n.TableName())
}

// Name 计算节点名称
func (n RealTimeNode) Name() string {
	prefix := n.NamePrefix
	if prefix == "" {
		prefix = n.GetNodeType()
	}
	name := fmt.Sprintf("%s(%s)", prefix, n.SourceRtId)
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

// Config 配置
func (n RealTimeNode) Config() map[string]interface{} {
	baseConfig := map[string]interface{}{
		"from_result_table_ids": []string{n.SourceRtId},
		"table_name":            n.TableName(),
		"output_name":           n.TableName(),
		"bk_biz_id":             n.BkBizId,
		"name":                  n.Name(),
		"window_type":           "none",
		"sql":                   n.Sql,
	}
	if n.AggInterval != 0 {
		baseConfig["window_type"] = "scroll"                                 // 滚动窗口
		baseConfig["waiting_time"] = config.GlobalBkdataRealtimeNodeWaitTime // 此时添加等待时间，是为了有可能数据延时的情况
		baseConfig["count_freq"] = n.AggInterval
	}
	return baseConfig
}

func (n RealTimeNode) GenStatisticSql(rtId, aggMethod string, metricFields, dimensionFields []string) string {
	fields := append(metricFields, dimensionFields...)
	selectStr := strings.Join(fields, ",")
	groupByStr := strings.Join(dimensionFields, ",")
	return fmt.Sprintf(`SELECT %s FROM %s GROUP BY %s`, selectStr, rtId, groupByStr)
}

// FilterUnknownTimeNode 过滤未来数据和过期数据
type FilterUnknownTimeNode struct {
	ExpireTime int
	FutureTime int
	RealTimeNode
}

func NewFilterUnknownTimeNode(sourceRtId string, aggInterval int, aggMethod string, metricFields, dimensionFields []string, sql, namePrefix, outputRtId string, parentList []Node) *FilterUnknownTimeNode {
	n := &FilterUnknownTimeNode{RealTimeNode: *NewRealTimeNode(sourceRtId, aggInterval, aggMethod, metricFields, dimensionFields, sql, namePrefix, outputRtId, parentList), ExpireTime: 3600, FutureTime: 60}
	return n
}

// TableName 输出表名（不带业务ID前缀）
func (n FilterUnknownTimeNode) TableName() string {
	if n.processRtId != "" {
		return n.processRtId
	}
	return fmt.Sprintf("%s_%s", n.ProcessRtId, config.GlobalBkdataRawTableSuffix)
}

func (n FilterUnknownTimeNode) GenStatisticSql(rtId, aggMethod string, metricFields, dimensionFields []string) string {
	var fields []string
	for _, field := range append(metricFields, dimensionFields...) {
		fields = append(fields, fmt.Sprintf("`%s`", field))
	}
	selectStr := strings.Join(fields, ",")
	return fmt.Sprintf(`SELECT %s FROM %s WHERE (time > UNIX_TIMESTAMP() - %d) AND (time < UNIX_TIMESTAMP() + %d)`, selectStr, rtId, n.ExpireTime, n.FutureTime)
}

// CMDBPrepareAggregateFullNode CMDB 预聚合，信息补充节点，1条对1条
type CMDBPrepareAggregateFullNode struct {
	RealTimeNode
}

func NewCMDBPrepareAggregateFullNode(sourceRtId string, aggInterval int, aggMethod string, metricFields, dimensionFields []string, sql, namePrefix, outputRtId string, parentList []Node) *CMDBPrepareAggregateFullNode {
	n := &CMDBPrepareAggregateFullNode{RealTimeNode: *NewRealTimeNode(sourceRtId, aggInterval, aggMethod, metricFields, dimensionFields, sql, namePrefix, outputRtId, parentList)}
	return n
}

// TableName 输出表名（不带业务ID前缀）
func (n CMDBPrepareAggregateFullNode) TableName() string {
	if n.processRtId != "" {
		return n.processRtId
	}

	processRtId := n.ProcessRtId[:strings.LastIndexAny(n.ProcessRtId, "_")]

	return fmt.Sprintf("%s_%s", processRtId, config.GlobalBkdataCMDBFullTableSuffix)
}

// Name 节点名
func (n CMDBPrepareAggregateFullNode) Name() string {
	return "添加主机拓扑关系数据"
}

// Config 配置
func (n CMDBPrepareAggregateFullNode) Config() map[string]interface{} {
	baseConfig := map[string]interface{}{
		"from_result_table_ids": []string{n.SourceRtId, CMDBHostTopRtId},
		"table_name":            n.TableName(),
		"output_name":           n.TableName(),
		"bk_biz_id":             n.BkBizId,
		"name":                  n.Name(),
		"window_type":           "none",
		"sql":                   n.Sql,
	}
	return baseConfig
}

func (n CMDBPrepareAggregateFullNode) GenStatisticSql(rtId, aggMethod string, metricFields, dimensionFields []string) string {
	var fields []string
	for _, field := range append(metricFields, dimensionFields...) {
		fields = append(fields, fmt.Sprintf("A.`%s`", field))
	}
	selectStr := strings.Join(append(fields, "B.bk_host_id", "B.bk_relations"), ",")
	return fmt.Sprintf(`SELECT %s FROM %s A LEFT JOIN %s B ON A.bk_target_cloud_id = B.bk_cloud_id and A.bk_target_ip = B.bk_host_innerip`, selectStr, rtId, CMDBHostTopRtId)
}

// CMDBPrepareAggregateSplitNode CMDB 预聚合，将补充的信息进行拆解，1条对多条
type CMDBPrepareAggregateSplitNode struct {
	RealTimeNode
}

func NewCMDBPrepareAggregateSplitNode(sourceRtId string, aggInterval int, aggMethod string, metricFields, dimensionFields []string, sql, namePrefix, outputRtId string, parentList []Node) *CMDBPrepareAggregateSplitNode {
	n := &CMDBPrepareAggregateSplitNode{RealTimeNode: *NewRealTimeNode(sourceRtId, aggInterval, aggMethod, metricFields, dimensionFields, sql, namePrefix, outputRtId, parentList)}
	return n
}

// TableName 输出表名（不带业务ID前缀）
func (n CMDBPrepareAggregateSplitNode) TableName() string {
	if n.processRtId != "" {
		return n.processRtId
	}

	processRtId := n.ProcessRtId[:strings.LastIndexAny(n.ProcessRtId, "_")]

	return fmt.Sprintf("%s_%s", processRtId, config.GlobalBkdataCMDBSplitTableSuffix)
}

// Name 节点名
func (n CMDBPrepareAggregateSplitNode) Name() string {
	return "拆分拓扑关系中模块和集群"
}

// Config 配置
func (n CMDBPrepareAggregateSplitNode) Config() map[string]interface{} {
	baseConfig := map[string]interface{}{
		"from_result_table_ids": []string{n.SourceRtId},
		"table_name":            n.TableName(),
		"output_name":           n.TableName(),
		"bk_biz_id":             n.BkBizId,
		"name":                  n.Name(),
		"window_type":           "none",
		"sql":                   n.Sql,
	}
	return baseConfig
}

func (n CMDBPrepareAggregateSplitNode) GenStatisticSql(rtId, aggMethod string, metricFields, dimensionFields []string) string {
	var fields []string
	for _, field := range append(metricFields, dimensionFields...) {
		fields = append(fields, fmt.Sprintf("`%s`", field))
	}
	selectStr := strings.Join(append(fields, "bk_host_id", "bk_relations", "bk_obj_id", "bk_inst_id"), ",")
	return fmt.Sprintf(`SELECT %s FROM %s ,lateral table(udf_bkpub_cmdb_split_set_module(bk_relations, bk_biz_id)) as T(bk_obj_id, bk_inst_id)`, selectStr, rtId)
}
