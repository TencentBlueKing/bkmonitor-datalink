// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.process;

import com.tencent.bk.bkmonitor.rum.aggregation.ViewAggregator;
import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.utils.RumEntityKey;
import com.tencent.bk.bkmonitor.rum.utils.RumEventIdentity;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.Map;
import java.util.UUID;
import javax.annotation.Nullable;
import org.apache.flink.api.common.typeinfo.TypeInformation;

/**
 * 按 (bkBizId, appName, viewId) 管理处理时间窗口的 View 聚合算子。
 *
 * <p>事件字段计算由 {@link ViewAggregator} 负责。本类只提供 View 状态的类型适配和窗口所需的 key 提取逻辑；
 * keyed state、处理时间 timer、有界修正 cleanup、增量输出和窗口关闭由
 * {@link AbstractProcessingTimeWindowFunction} 统一实现。
 */
public class ViewProcessFunction
    extends AbstractProcessingTimeWindowFunction<String, RumEvent, ViewEventDocument, ViewEventDocument> {
  private static final long serialVersionUID = 1L;
  private static final String WINDOW_SCOPE = "view";
  private static final StateNames STATE_NAMES =
      new StateNames(
          "view-stats",
          "view-emit-timer",
          "view-processing-gap-timer",
          "view-processing-maxlife-timer",
          "view-dirty",
          "view-active-key-counted");

  public ViewProcessFunction(long emitMs, long gapMs, long maxLifeMs, long stateTtlMs) {
    super(
        emitMs,
        gapMs,
        maxLifeMs,
        stateTtlMs,
        TypeInformation.of(ViewEventDocument.class),
        STATE_NAMES,
        WINDOW_SCOPE);
  }

  /** 判定是否为带 view.id 的 span(任何携带 view 上下文的 span 都参与 view 聚合)。 */
  public static boolean hasViewId(RumEvent event) {
    if (event == null || event.getSpan() == null) {
      return false;
    }
    Map<String, Object> attrs = event.getSpan().getAttributes();
    if (attrs == null) {
      return false;
    }
    Object viewId = attrs.get("view.id");
    return viewId != null && !viewId.toString().trim().isEmpty();
  }

  /** 提取文档中的原始 view.id，调用前须通过 {@link #hasViewId} 过滤。 */
  public static String viewIdOf(RumEvent event) {
    return event.getSpan().getAttributes().get("view.id").toString();
  }

  /** 在业务和应用范围内分区，避免不同租户的同名 View 共用状态。 */
  public static String viewKeyOf(RumEvent event) {
    return RumEntityKey.of(event.getSpan(), viewIdOf(event));
  }

  @Override
  @Nullable
  protected String eventIdentity(RumEvent event) {
    return RumEventIdentity.of(event);
  }

  @Override
  protected long eventTimestampMillis(RumEvent event) {
    return event.getEventTime() / TimeUtils.US_TO_MS;
  }

  @Override
  protected ViewEventDocument createAccumulator(RumEvent event) {
    ViewEventDocument accumulator = new ViewEventDocument();
    accumulator.setViewId(viewIdOf(event));
    accumulator.setWindowId(UUID.randomUUID().toString());
    return accumulator;
  }

  @Override
  protected void addEvent(ViewEventDocument accumulator, RumEvent event) {
    ViewAggregator.add(accumulator, event);
  }

  @Override
  protected boolean isClosed(ViewEventDocument accumulator) {
    return accumulator.isClosed();
  }

  @Override
  protected void markClosed(ViewEventDocument accumulator) {
    accumulator.setClosed(true);
  }

  @Override
  protected boolean shouldCloseImmediately(ViewEventDocument accumulator) {
    return accumulator.isNormalCloseSeen();
  }

  @Override
  protected ViewEventDocument toIncrementalSnapshot(
      ViewEventDocument accumulator, long timestamp) {
    return accumulator.toSummary(timestamp, false);
  }

  @Override
  protected ViewEventDocument toFinalSnapshot(
      ViewEventDocument accumulator, long timestamp, String closeReason) {
    if (!accumulator.isClosed()) {
      accumulator.setCloseReason(closeReason);
    }
    return accumulator.toSummary(timestamp, true);
  }
}
