// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.process;

import com.tencent.bk.bkmonitor.rum.aggregation.SessionAggregator;
import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEventDocument;
import com.tencent.bk.bkmonitor.rum.utils.RumEntityKey;
import com.tencent.bk.bkmonitor.rum.utils.RumEventIdentity;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.UUID;
import javax.annotation.Nullable;
import org.apache.flink.api.common.state.MapState;
import org.apache.flink.api.common.state.MapStateDescriptor;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.util.OutputTag;

/**
 * 按 (bkBizId, appName, sessionId) 做活动会话聚合的窗口算子。
 *
 * <p>事件字段和计数由 {@link SessionAggregator} 负责，窗口生命周期由
 * {@link AbstractProcessingTimeWindowFunction} 负责。本算子只提供两者之间的类型适配和 Session 快照语义。
 *
 * <p>{@link #LATE_EVENTS} 承载 Session 身份缺失、closed-window 修正和 cleanup 后极迟事件，
 * 供下游审计。
 */
public class SessionizeProcessFunction
    extends AbstractProcessingTimeWindowFunction<
        String, RumEvent, SessionEventDocument, SessionEvent> {
  private static final long serialVersionUID = 1L;
  private static final String VIEW_HASH_WORDS_STATE_NAME = "session-view-hash-words";
  private static final String WINDOW_SCOPE = "session";
  private static final int DEFAULT_MAX_STRING_CHARS = 512;
  private static final StateNames STATE_NAMES =
      new StateNames(
          "session-accumulator",
          "session-emit-timer",
          "session-gap-timer",
          "session-processing-maxlife-timer",
          "session-dirty",
          "session-active-key-counted");

  /** Session 拒绝或修正事件的审计 side output 标签。 */
  public static final OutputTag<RumEvent> LATE_EVENTS =
      new OutputTag<RumEvent>("late-rum-events") {
        private static final long serialVersionUID = 1L;
      };

  private final int maxStringChars;
  private transient MapState<Integer, Long> viewHashWordsState;

  public SessionizeProcessFunction(long emitMs, long gapMs, long maxLifeMs, long stateTtlMs) {
    this(emitMs, gapMs, maxLifeMs, stateTtlMs, DEFAULT_MAX_STRING_CHARS);
  }

  public SessionizeProcessFunction(
      long emitMs, long gapMs, long maxLifeMs, long stateTtlMs, int maxStringChars) {
    super(
        emitMs,
        gapMs,
        maxLifeMs,
        stateTtlMs,
        TypeInformation.of(SessionEventDocument.class),
        STATE_NAMES,
        WINDOW_SCOPE);
    if (maxStringChars <= 0) {
      throw new IllegalArgumentException("session string limit must be positive");
    }
    this.maxStringChars = maxStringChars;
  }

  /** 在业务和应用范围内分区；调用前须通过 {@link RumEvent#hasSessionAndTime()} 过滤。 */
  public static String sessionKeyOf(RumEvent event) {
    return RumEntityKey.of(event.getSpan(), event.getSessionId());
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
  protected OutputTag<RumEvent> lateEventOutputTag() {
    return LATE_EVENTS;
  }

  @Override
  protected void openAdditionalState() {
    MapStateDescriptor<Integer, Long> descriptor =
        new MapStateDescriptor<>(VIEW_HASH_WORDS_STATE_NAME, Integer.class, Long.class);
    viewHashWordsState = getRuntimeContext().getMapState(descriptor);
  }

  @Override
  protected void clearAdditionalState() throws Exception {
    viewHashWordsState.clear();
  }

  @Override
  protected SessionEventDocument createAccumulator(RumEvent event) {
    SessionEventDocument accumulator = new SessionEventDocument(maxStringChars);
    accumulator.setSessionId(event.getSessionId());
    accumulator.setWindowId(UUID.randomUUID().toString());
    return accumulator;
  }

  @Override
  protected void addEvent(SessionEventDocument accumulator, RumEvent event) throws Exception {
    int previousViewBucket = accumulator.getLastViewBucket();
    SessionAggregator.add(accumulator, event);
    int currentViewBucket = accumulator.getLastViewBucket();
    if (currentViewBucket < 0 || currentViewBucket == previousViewBucket) {
      return;
    }

    int index = currentViewBucket >>> 6;
    long mask = 1L << (currentViewBucket & 63);
    Long storedWord = viewHashWordsState.get(index);
    long word = storedWord == null ? 0L : storedWord;
    if ((word & mask) != 0L) {
      return;
    }
    viewHashWordsState.put(index, word | mask);
    accumulator.setViewCount(saturatingIncrement(accumulator.getViewCount()));
  }

  private static int saturatingIncrement(int value) {
    return value == Integer.MAX_VALUE ? value : value + 1;
  }

  @Override
  protected boolean isClosed(SessionEventDocument accumulator) {
    return accumulator.isClosed();
  }

  @Override
  protected void markClosed(SessionEventDocument accumulator) {
    accumulator.setClosed(true);
  }

  @Override
  protected boolean shouldCloseImmediately(SessionEventDocument accumulator) {
    return accumulator.isNormalCloseSeen();
  }

  @Override
  protected SessionEvent toIncrementalSnapshot(
      SessionEventDocument accumulator, long timestamp) {
    return accumulator.toSummary(timestamp, false, null);
  }

  @Override
  protected SessionEvent toFinalSnapshot(
      SessionEventDocument accumulator, long timestamp, String closeReason) {
    if (!accumulator.isClosed()) {
      accumulator.setCloseReason(closeReason);
    }
    return accumulator.toSummary(timestamp, true, accumulator.getCloseReason());
  }
}
