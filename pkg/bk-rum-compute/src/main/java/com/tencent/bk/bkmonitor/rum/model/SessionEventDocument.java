// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import com.tencent.bk.bkmonitor.rum.aggregation.SessionAggregator;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import javax.annotation.Nullable;
import lombok.Data;

/**
 * 单个 session 的可变状态，保存在 Flink keyed state 中(按 sessionId 分区)。
 *
 * <p>状态字段只描述运行期累计结果；事件如何折叠由 {@link SessionAggregator} 负责，输出快照仍由
 * {@link #toSummary(long, boolean, String)} 负责。这样状态结构不会和窗口生命周期或事件分类逻辑互相渗透。
 */
@Data
public class SessionEventDocument {
  private static final int DEFAULT_MAX_STRING_CHARS = 512;

  private int maxStringChars = DEFAULT_MAX_STRING_CHARS;

  @Nullable
  private String sessionId;

  /** 窗口创建时生成并随状态持久化，清理后重开时使用新 ID。 */
  @Nullable
  private String windowId;

  /** 窗口首次事件确定的 UTC 索引日期，后续乱序事件不得改变物理索引路由。 */
  @Nullable
  private String indexDate;

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

  // ---- 标识与维度 ----

  @Nullable
  private String browserVersion;
  @Nullable
  private Integer bkBizId;
  @Nullable
  private String userId;

  /** 会话内最早事件时间（微秒 epoch）。 */
  private long minStartTime;

  /** 会话内最晚事件时间（微秒 epoch）。 */
  private long maxEndTime;

  /** 会话内最近一次事件时间（微秒 epoch）。 */
  private long lastEventTs;

  /** session 内唯一 view.id 数量的近似值（CSV: {@code view_count}）。 */
  private int viewCount;
  /** Session 内 Span Link 的数量，不去重（CSV: {@code trace_count}）。 */
  private int traceCount;
  private long actionCount;
  private long errorCount;
  private long resourceCount;
  private long longTaskCount;

  /** 挫败行为数。 */
  private long frustrationCount = 0;

  @Nullable
  private String enterUrlTemplate;
  @Nullable
  private String exitUrlTemplate;
  @Nullable
  private String deviceType;
  @Nullable
  private String browser;
  @Nullable
  private String os;
  @Nullable
  private String networkEffectiveType;

  private int requestCount;
  private int requestErrorCount;

  /** 最近处理的 view hash 桶，连续同 View 事件可跳过独立 bitmap state 读取。 */
  private int lastViewBucket = -1;

  /** 有效 View 加载耗时之和（微秒），用于计算 Session 平均值。 */
  private long totalLoadTimeUs;
  private long loadTimeSampleCount;
  private long maxLcpUs;
  private double maxCls;
  private long maxInpUs;

  private boolean closed;

  /** 首次窗口关闭时间（微秒），后续迟到修正保持不变。 */
  private long closeTimeUs;

  /** SDK 主动上报过 phase=end 事件，用于区分「正常关闭」与「预计算超时」。 */
  private boolean normalCloseSeen;

  /** 首次关窗的触发原因，迟到修正保留该值以区分业务结束和运行期超时。 */
  @Nullable
  private String closeReason;

  /** 使用默认字符串长度上限创建状态；同时作为 Flink POJO 反序列化入口。 */
  public SessionEventDocument() {}

  /**
   * 创建带指定字符串长度上限的状态。
   *
   * @param maxStringChars 状态中单个字符串允许保留的最大字符数
   */
  public SessionEventDocument(int maxStringChars) {
    if (maxStringChars <= 0) {
      throw new IllegalArgumentException("session string limit must be positive");
    }
    this.maxStringChars = maxStringChars;
  }

  /**
   * 兼容旧调用方，把事件交给 Session 业务聚合器处理。
   *
   * @param event 待折叠的 RUM 事件
   */
  public void add(RumEvent event) {
    SessionAggregator.add(this, event);
  }

  /** 将累加状态导出为增量或最终的 {@link SessionEvent}。 */
  public SessionEvent toSummary(long updatedAtTs, boolean closed) {
    return toSummary(updatedAtTs, closed, null);
  }

  /**
   * 将累加状态导出为增量或最终的 {@link SessionEvent}。
   *
   * @param updatedAtTs 本次触发的处理时间（毫秒，Flink timer 域），输出时转为微秒
   * @param closed 是否为最终汇总
   * @param closeReason 关闭原因，会写入 {@code close_reason}；增量快照应传 {@code null}
   * @return 输出快照
   */
  public SessionEvent toSummary(long updatedAtTs, boolean closed, @Nullable String closeReason) {
    SessionEvent summary = new SessionEvent();
    summary.setSessionId(sessionId);
    summary.setWindowId(windowId);
    summary.setDate(indexDate);
    summary.setAppName(appName);
    summary.setServiceName(serviceName);
    summary.setSdkName(sdkName);
    summary.setSdkLang(sdkLang);
    summary.setSdkVersion(sdkVersion);
    summary.setBrowserVersion(browserVersion);
    summary.setBkBizId(bkBizId);
    summary.setEnvironment(environment);
    summary.setServiceVersion(serviceVersion);
    summary.setUserId(userId);
    summary.setMinStartTime(minStartTime);
    summary.setMaxEndTime(maxEndTime);
    summary.setDuration(Math.max(0L, maxEndTime - minStartTime));
    summary.setViewCount(viewCount);
    summary.setTraceCount(traceCount);
    summary.setRequestCount(requestCount);
    summary.setRequestErrorCount(requestErrorCount);
    summary.setActionCount(actionCount);
    summary.setErrorCount(errorCount);
    summary.setResourceCount(resourceCount);
    summary.setLongTaskCount(longTaskCount);
    summary.setFrustrationCount(frustrationCount);
    summary.setEnterUrlTemplate(enterUrlTemplate);
    summary.setExitUrlTemplate(exitUrlTemplate);
    summary.setDeviceType(deviceType);
    summary.setBrowser(browser);
    summary.setOs(os);
    summary.setNetworkEffectiveType(networkEffectiveType);
    summary.setEndReason(closeReason != null ? closeReason : (closed ? "timeout" : null));
    summary.setAvgLoadTimeUs(
        loadTimeSampleCount == 0L ? 0L : totalLoadTimeUs / loadTimeSampleCount);
    summary.setMaxLcpUs(maxLcpUs);
    summary.setMaxCls(maxCls);
    summary.setMaxInpUs(maxInpUs);
    summary.setHasReplay(false);
    summary.setActive(!closed);
    summary.setClosed(closed);
    summary.setUpdatedAtTs(updatedAtTs * TimeUtils.MS_TO_US);
    if (closed && closeTimeUs == 0L) {
      closeTimeUs = updatedAtTs * TimeUtils.MS_TO_US;
    }
    summary.setClosTim(closed ? closeTimeUs : 0L);
    return summary;
  }

  /** 保留旧调用方式:生成最终汇总。 */
  public SessionEvent toSummary(long updatedAtTs) {
    return toSummary(updatedAtTs, true);
  }
}
