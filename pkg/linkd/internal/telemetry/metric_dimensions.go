// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import "github.com/prometheus/otlptranslator"

func metricDimension(key string) (MetricDimension, bool) {
	// 公共词表说明维度含义；每个 instrument 在自身声明处选择实际使用的子集。
	descriptions := map[string]string{
		"linkd.stage":               "处理阶段，例如 clean、lifecycle",
		"messaging.system":          "消息或输出传输类型，例如 kafka、redis_stream、http；部分观察路径省略",
		"linkd.event_source_id":     "告警源 ID；仅在观察器已知来源时携带，不包含租户或告警身份",
		"linkd.outcome":             "本次执行或操作的分类结果；枚举由各观察器约束",
		"linkd.trigger":             "触发方式，当前为 queue",
		"linkd.reason_code":         "结构化原因码；仅在有原因或背压转换时携带",
		"linkd.queue.role":          "队列职责，例如 clean_signal、lifecycle_signal",
		"messaging.kafka.partition": "Kafka 分区号；仅 Kafka 且可解析 lane 时携带",
		"linkd.settlement.mode":     "消息确认方式，individual 或 cumulative",
		"linkd.action":              "运行时动作，例如 start、stop、pause、resume",
		"linkd.step":                "Cleaner 可靠性步骤，例如 transform、source_ack",
		"linkd.event.action":        "领域事件动作",
		"linkd.event.state":         "事件处理后的状态",
		"linkd.operation":           "固定操作名称；具体枚举由所属组件定义",
		"linkd.hook.name":           "输出钩子结果名称；为空时使用配置名，再回退为 unknown",
		"linkd.status":              "丰富执行状态，例如 succeeded、partial、failed",
		"linkd.chain_kind":          "丰富链类型，configured、noop 或 unknown",
		"linkd.processor":           "丰富处理器类别，不使用配置中的任意 ID",
		"linkd.diagnostic_code":     "经过归一化的丰富诊断码",
		"linkd.dependency":          "经过归一化的依赖类型",
		"linkd.datasource":          "丰富数据源类别",
		"linkd.task":                "固定控制面管理任务名称",
		"linkd.object.type":         "存储领域对象类型",
		"linkd.dispatch.side":       "调度观察侧，controller 或 worker",
		"linkd.task.role":           "任务角色，cleaner 或 lifecycle",
		"linkd.task.from":           "任务转换前的阶段，初始为空时记录 none",
		"linkd.task.phase":          "任务阶段，例如 preparing、running、stopping、stopped",
		"linkd.reason":              "调度状态转换原因",
		"linkd.worker.state":        "工作会话状态，healthy、stale、draining 或 cooldown",
		"linkd.replica.kind":        "副本统计口径，matching、target、running 或 shortage",
		"linkd.metadata.state":      "Kafka 元数据状态，ready、waiting 或 error",
		"linkd.batch_kind":          "ES 物理批次类型，read 或 write",
		"linkd.batch_phase":         "固定批次诊断阶段名称；各阶段口径不同，不可直接相加",
		"linkd.batch_trigger":       "聚合器提交触发原因",
		"linkd.metric_schema":       "耗时桶口径版本，当前为 2；查询历史窗口时避免混算",
	}
	description, ok := descriptions[key]
	if !ok {
		return MetricDimension{}, false
	}
	namer := otlptranslator.LabelNamer{UTF8Allowed: false}
	name, err := namer.Build(key)
	if err != nil {
		return MetricDimension{}, false
	}
	return MetricDimension{Name: key, PrometheusName: name, Description: description}, true
}
