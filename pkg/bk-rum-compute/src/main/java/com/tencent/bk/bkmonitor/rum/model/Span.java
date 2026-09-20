// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import com.fasterxml.jackson.annotation.JsonAlias;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.List;
import java.util.Map;
import javax.annotation.Nullable;
import lombok.Data;

/**
 * RUM 上报数据中的单条 Span,字段命名遵循 RUM SDK 上行 JSON 协议。
 *
 * <p>一个会话通常包含多条 span(view / resource / action / error 等),通过 {@link #traceId} + {@link #parentSpanId}
 * 组成追踪树,最终由 Flink 任务聚合写入存储。
 */
@Data
public class Span {

  /** 应用名,与上报侧 RUM SDK 配置一致。 */
  @Nullable
  @JsonProperty("app_name")
  private String appName;

  /** span 维度属性,键值对集合(如 http.url、error.type 等),具体取值因 span 类型而异。 */
  @Nullable
  @JsonProperty("attributes")
  private Map<String, Object> attributes;

  /** 蓝鲸业务 ID,用于按业务隔离数据。 */
  @Nullable
  @JsonProperty("bk_biz_id")
  private Integer bkBizId;

  /** 客户端 IP,可用于地域/网络维度的离线分析。 */
  @Nullable
  @JsonProperty("client_ip")
  private String clientIp;

  /** span 耗时(ns 或 μs,与 SDK 端协议保持一致)。 */
  @Nullable
  @JsonProperty("elapsed_time")
  private Long elapsedTime;

  /** span 结束时间戳。 */
  @Nullable
  @JsonProperty("end_time")
  private Long endTime;

  /** span 内的事件列表(如 exception 事件),具体结构由 SDK 端定义。 */
  @Nullable
  @JsonProperty("events")
  private List<Object> events;

  /** 迭代序号,标识同一 span 在批量上报中的次序。 */
  @Nullable
  @JsonProperty("iterationindex")
  private Integer iterationindex;

  /** span 类型码,对应 OTel SpanKind 枚举(如 1=INTERNAL、3=CLIENT 等)。 */
  @Nullable
  @JsonProperty("kind")
  private Integer kind;

  /** 关联链接列表,指向其他 trace/span 的引用。 */
  @Nullable
  @JsonProperty("links")
  private List<SpanLink> links;

  /** 父 span ID,根 span 时为空。 */
  @Nullable
  @JsonProperty("parent_span_id")
  private String parentSpanId;

  /** 资源段,描述产生该 span 的服务/设备/SDK 等元信息。 */
  @Nullable
  @JsonProperty("resource")
  private Resource resource;

  /** 当前 span 的唯一 ID。 */
  @Nullable
  @JsonProperty("span_id")
  private String spanId;

  /** span 名称(如 {@code view.load} / {@code resource.fetch}),描述其代表的具体操作。 */
  @Nullable
  @JsonProperty("span_name")
  private String spanName;

  /** span 起始时间戳。 */
  @Nullable
  @JsonProperty("start_time")
  private Long startTime;

  /** span 执行状态,见 {@link SpanStatus}。 */
  @Nullable
  @JsonAlias("spanStatus")
  @JsonProperty("status")
  private SpanStatus status;

  /** 所属 trace ID,同 trace 内的所有 span 共享该字段。 */
  @Nullable
  @JsonProperty("trace_id")
  private String traceId;

  /** trace 状态(vendor 扩展信息),键值对。 */
  @Nullable
  @JsonProperty("trace_state")
  private Map<String, Object> traceState;
}
