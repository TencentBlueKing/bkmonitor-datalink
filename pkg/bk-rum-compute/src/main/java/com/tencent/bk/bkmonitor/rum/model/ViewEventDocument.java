// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import javax.annotation.Nullable;
import lombok.Data;

/**
 * 一个 View 的活动统计宽文档,写入 Elasticsearch 的 {@code rum-view-wide-*} 索引。
 *
 * <p>窗口关闭时输出 {@code closed=true}，并将聚合与事件身份状态保留到配置的 cleanup timer
 * 到期；期间未见过的唯一事件可以修正最终快照，相同事件被去重。cleanup 后，在有界清理标记
 * 保留期内拒绝落后于 watermark 的事件；允许重开时生成新的 {@code windowId}。
 */
@Data
public class ViewEventDocument {

  /** 原始 View 业务 ID，同一 View 的不同窗口实例由 {@link #windowId} 区分。 */
  @Nullable
  private String viewId;

  /** 窗口创建时生成并随状态持久化，所有快照复用，清理后重开时使用新 ID。 */
  @Nullable
  private String windowId;

  /** 所属 session。 */
  @Nullable
  private String sessionId;

  /** rum 应用名称 */
  @Nullable
  private String appName;

  /** 服务名（CSV: {@code resource.service.name}）。 */
  @Nullable
  private String serviceName;

  /** 服务版本（CSV: {@code resource.service.version}）。 */
  @Nullable
  private String serviceVersion;

  /** 部署环境（CSV: {@code resource.deployment.environment.name}）。 */
  @Nullable
  private String environment;

  /** Telemetry SDK 名称（CSV: {@code resource.telemetry.sdk.name}）。 */
  @Nullable
  private String sdkName;

  /** Telemetry SDK 语言（CSV: {@code resource.telemetry.sdk.language}）。 */
  @Nullable
  private String sdkLang;

  /** Telemetry SDK 版本（CSV: {@code resource.telemetry.sdk.version}）。 */
  @Nullable
  private String sdkVersion;

  /** 蓝鲸业务id */
  @Nullable
  private Integer bkBizId;

  /** 用户 ID（CSV: {@code attributes.user.id}）。 */
  @Nullable
  private String userId;

  /** 浏览器名称（CSV: {@code resource.user_agent.name}）。 */
  @Nullable
  private String browser;

  /** 浏览器版本（CSV: {@code resource.user_agent.version}）。 */
  @Nullable
  private String browserVersion;

  /** 操作系统名称（CSV: {@code resource.user_agent.os.name}）。 */
  @Nullable
  private String os;

  /** 设备类型（CSV: {@code resource.device.type}）。 */
  @Nullable
  private String deviceType;

  /** 网络有效类型，如 slow-2g、2g、3g、4g。 */
  @Nullable
  private String networkType;

  /** View 名称。 */
  @Nullable
  private String viewName;

  /** View 加载类型。 */
  @Nullable
  private String viewLoadingType;

  /** View 加载耗时(微秒 epoch)。 */
  @Nullable
  private Long viewLoadingTime;

  /** View 加载耗时来源。 */
  @Nullable
  private String viewLoadingTimeSource;

  /** SDK 上报的视图结束原因（来自 {@code attributes.view.end_reason}，值依 SDK 协议而异）。 */
  @Nullable
  private String viewEndReason;

  /** SDK 主动上报过 view.phase=end 事件，用于区分正常关闭与预计算超时（与 Session 的 session.phase 同源）。 */
  private boolean normalCloseSeen;


  /**
   * 首次关闭窗口的触发原因，迟到修正不改变该值：
   *
   * <ul>
   *   <li>{@code normal}：观察到 {@code view.phase == "end"} 时立即关闭
   *   <li>{@code idle_timeout}：连续无新事件达到 gap
   *   <li>{@code max_life}：达到配置的最大生命周期
   * </ul>
   *
   * <p>{@link #viewEndReason} 保留 SDK 的 {@code view.end_reason} 原始语义；下游窗口状态应使用
   * 本字段。
   */
  @Nullable
  private String closeReason;

  /** View 开始时间（微秒 epoch），来自 SDK 上报的 {@code view.started_at}。 */
  @Nullable
  private Long minStartTime;

  /** 窗口首次事件确定的 UTC 索引日期，后续乱序事件不得改变物理索引路由。 */
  @Nullable
  private String indexDate;

  /**
   * 前一个 View URL视图路径分组
   */
  @Nullable
  private String previousUrlTemplate;

  /**
   * 视图路径分组
   */
  @Nullable
  private String urlTemplate;


  /** Load Event 时间 */
  private long actionCount = 0;

  /** 资源数 */
  private long resourceCount = 0;

  /** 错误类事件计数(status.code!=0 或 span_type=error 或 span_name 含 error)。 */
  private long errorCount = 0;

  /** 请求数 */
  private long requestCount = 0;

  /** XHR / Fetch 请求中错误或超时数量。 */
  private long requestErrorCount = 0;

  /** 长任务数 */
  private long longTaskCount = 0;

  /** 挫败行为数 */
  private long frustrationCount = 0;


  /** 视图事件版本号。 */
  @Nullable
  private Long version;

  /** fcp 指标（微秒）。null 表示未设置。 */
  @Nullable
  private Long fcp;

  /** cls 指标（无量纲布局偏移分数）。 */
  private double cls = 0D;

  /** inp 指标（微秒） */
  private long inp = 0;

  /** lcp 指标（微秒） */
  private long lcp = 0;

  /** First Byte 指标（微秒）。null 表示未设置。 */
  @Nullable
  private Long ttfb;

  /** 最近一次事件时间（微秒 epoch）。 */
  private long lastEventTs;

  /** View 内最晚 span 结束时间（微秒 epoch）。 */
  private long maxEndTime;

  /** 本次输出时间（微秒 epoch）。 */
  private long updatedAtTs;

  /** 输出文档使用的稳定 UTC 索引日期(yyyy-MM-dd)。 */
  @Nullable
  private String date;

  /** 平台派生字段：View 阶段不是 end 时表示仍活跃。 */
  private boolean active;

  /** 是否为会话关闭时的最终汇总(true)或 30s 增量(false)。 */
  private boolean closed;

  /** 会话是否包含回放，当前协议固定为 false（CSV: {@code attributes.session.has_replay}）。 */
  private boolean hasReplay;

  /** View 内 Span Link 的数量，不去重（CSV: {@code trace_count}）。 */
  private int traceCount;

  /** View 持续时长（微秒，CSV: {@code duration}）。 */
  private long duration;

  /** 首次窗口关闭时间（微秒），后续迟到修正保持不变。 */
  private long closeTimeUs;

  /** 窗口关闭时间 */
  private long closTim;

  /** 由运行态聚合输出副本，填入 closed / updatedAtTs / date / closTim。 */
  public ViewEventDocument toSummary(long updatedAtTs, boolean closed) {
    ViewEventDocument s = new ViewEventDocument();
    s.setViewId(viewId);
    s.setWindowId(windowId);
    s.setSessionId(sessionId);
    s.setAppName(appName);
    s.setServiceName(serviceName);
    s.setServiceVersion(serviceVersion);
    s.setEnvironment(environment);
    s.setSdkName(sdkName);
    s.setSdkLang(sdkLang);
    s.setSdkVersion(sdkVersion);
    s.setBkBizId(bkBizId);
    s.setUserId(userId);
    s.setBrowser(browser);
    s.setBrowserVersion(browserVersion);
    s.setOs(os);
    s.setDeviceType(deviceType);
    s.setNetworkType(networkType);
    s.setViewName(viewName);
    s.setUrlTemplate(urlTemplate);
    s.setViewLoadingType(viewLoadingType);
    s.setViewLoadingTime(viewLoadingTime);
    s.setViewLoadingTimeSource(viewLoadingTimeSource);
    s.setViewEndReason(viewEndReason);
    s.setCloseReason(closeReason);
    s.setMinStartTime(minStartTime);
    s.setIndexDate(indexDate);
    s.setPreviousUrlTemplate(previousUrlTemplate);
    s.setActionCount(actionCount);
    s.setResourceCount(resourceCount);
    s.setErrorCount(errorCount);
    s.setRequestCount(requestCount);
    s.setRequestErrorCount(requestErrorCount);
    s.setLongTaskCount(longTaskCount);
    s.setFrustrationCount(frustrationCount);
    s.setVersion(version);
    s.setFcp(fcp);
    s.setCls(cls);
    s.setInp(inp);
    s.setLcp(lcp);
    s.setTtfb(ttfb);
    s.setLastEventTs(lastEventTs);
    s.setMaxEndTime(maxEndTime);
    s.setUpdatedAtTs(updatedAtTs * TimeUtils.MS_TO_US);
    s.setDate(indexDate);
    s.setClosed(closed);
    s.setActive(!closed);
    s.setHasReplay(false);
    s.setTraceCount(traceCount);
    s.setDuration(
        minStartTime != null
            ? Math.max(0L, maxEndTime - minStartTime)
            : 0L);
    if (closed && closeTimeUs == 0L) {
      closeTimeUs = updatedAtTs * TimeUtils.MS_TO_US;
    }
    s.setCloseTimeUs(closeTimeUs);
    s.setClosTim(closed ? closeTimeUs : 0L);
    return s;
  }
}
