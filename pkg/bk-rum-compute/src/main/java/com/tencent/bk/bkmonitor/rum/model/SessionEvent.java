// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import javax.annotation.Nullable;
import lombok.Data;

/**
 * 一个会话的预聚合宽文档,也是最终写入 Elasticsearch 的文档结构。
 *
 * <p>由 {@link SessionEventDocument#toSummary(long)} 在 session 触发输出时生成。ES document id
 * 由租户与会话身份摘要拼接 {@code windowId}，同一窗口的迟到修正覆盖原文档，重开窗口独立保存。
 */
@Data
public class SessionEvent {

  // ---- 标识与维度 ----
  @Nullable
  private String sessionId;

  /** 同一窗口的所有快照共享此 ID，用于区分同一会话的不同窗口实例。 */
  @Nullable
  private String windowId;

  /**
   * 窗口首次事件确定的稳定 UTC 索引日期(yyyy-MM-dd)。
   *
   * <p>后续乱序事件可以更新 {@link #minStartTime}，但不会迁移已经输出的物理索引。
   */
  @Nullable
  private String date;

  // ---- 应用 / 环境 ----

  @Nullable
  private String appName;
  @Nullable
  private String serviceName;
  @Nullable
  private String sdkName;
  @Nullable
  private String sdkLang;
  @Nullable
  private String sdkVersion;
  @Nullable
  private String environment;
  @Nullable
  private String serviceVersion;

  @Nullable
  private Integer bkBizId;
  @Nullable
  private String userId;
  @Nullable
  private String browserVersion;

  // ---- 时间范围（微秒 epoch） ----
  private long minStartTime;
  private long maxEndTime;

  /** 会话持续时长（微秒，CSV: {@code duration}）。 */
  private long duration;

  /** 文档最近一次更新时间(微秒,增量或最终输出的处理时间戳)。 */
  private long updatedAtTs;

  /** 是否包含回放，当前协议固定为 false。 */
  private boolean hasReplay;

  /** 当前快照是否仍处于活动窗口。 */
  private boolean active;

  /** 是否为窗口最终快照。 */
  private boolean closed;

  /** 窗口关闭时间(微秒,处理时间戳),增量快照为 0。 */
  private long closTim;

  // ---- 事件计数 ----
  /** Session 内不同 view.id 的数量（CSV: {@code view_count}）。 */
  private int viewCount;

  /** Session 内 Span Link 的数量，不去重（CSV: {@code trace_count}）。 */
  private int traceCount;

  /** Session 内 XHR/fetch 请求数量（ES: {@code request_count}）。 */
  private int requestCount;

  /** Session 内 XHR/fetch 的错误或超时数量（ES: {@code request_error_count}）。 */
  private int requestErrorCount;

  /** 用户交互次数（ES: {@code action_count}）。 */
  private long actionCount;

  /** 所有错误类事件总数（ES: {@code error_count}）。 */
  private long errorCount;

  /** Session 内所有 resource 事件数量（ES: {@code resource_count}）。 */
  private long resourceCount;

  /** Long Task 数（ES: {@code long_task_count}）。 */
  private long longTaskCount;

  /** 挫败行为数量（ES: {@code frustration_count}）。 */
  private long frustrationCount;

  // ---- 页面轨迹 ----

  /** 入口视图 URL 模板（CSV: {@code enter_url_template}）。 */
  @Nullable
  private String enterUrlTemplate;

  /** 退出视图 URL 模板（CSV: {@code exit_url_template}）。 */
  @Nullable
  private String exitUrlTemplate;

  // ---- 设备与环境 ----
  @Nullable
  private String deviceType;
  @Nullable
  private String browser;
  @Nullable
  private String os;

  /** 有效网络质量（CSV: {@code network.effective_type},源：{@code navigator.connection.effectiveType}）。 */
  @Nullable
  private String networkEffectiveType;

  /** 会话关闭原因（View/Session 索引字段：{@code close_reason}）。 */
  @Nullable
  private String endReason;

  // ---- Web Vitals 聚合(取会话内最差值) ----
  /** 平均页面加载耗时（微秒）= 总加载耗时 / 有效样本数。 */
  private long avgLoadTimeUs;

  /** 会话内最大 LCP（微秒）。 */
  private long maxLcpUs;

  /** 会话内最大 CLS。 */
  private double maxCls;

  /** 会话内最大 INP（微秒）。 */
  private long maxInpUs;
}
