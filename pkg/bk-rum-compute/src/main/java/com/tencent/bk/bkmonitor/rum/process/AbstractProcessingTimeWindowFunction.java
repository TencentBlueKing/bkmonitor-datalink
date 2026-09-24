// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.process;

import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.io.Serializable;
import java.util.Objects;
import javax.annotation.Nullable;
import org.apache.flink.api.common.functions.OpenContext;
import org.apache.flink.api.common.state.MapState;
import org.apache.flink.api.common.state.MapStateDescriptor;
import org.apache.flink.api.common.state.ValueState;
import org.apache.flink.api.common.state.ValueStateDescriptor;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.metrics.Counter;
import org.apache.flink.runtime.state.FunctionInitializationContext;
import org.apache.flink.runtime.state.FunctionSnapshotContext;
import org.apache.flink.streaming.api.TimeDomain;
import org.apache.flink.streaming.api.TimerService;
import org.apache.flink.streaming.api.checkpoint.CheckpointedFunction;
import org.apache.flink.streaming.api.functions.KeyedProcessFunction;
import org.apache.flink.util.Collector;
import org.apache.flink.util.OutputTag;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 处理时间活动窗口的通用生命周期实现。
 *
 * <p>窗口保存 emit、gap、max-life、cleanup 和清理标记的截止时间，每个 key 仅注册最早截止时间的
 * processing-time timer；具体事件如何折叠及如何生成输出快照由子类提供。
 */
abstract class AbstractProcessingTimeWindowFunction<K, E, A, O>
    extends KeyedProcessFunction<K, E, O> implements CheckpointedFunction {
  private static final long serialVersionUID = 1L;
  private static final Logger LOG =
      LoggerFactory.getLogger(AbstractProcessingTimeWindowFunction.class);

  private final long emitMs;
  private final long gapMs;
  private final long maxLifeMs;
  private final long stateTtlMs;
  private final TypeInformation<A> accumulatorType;
  private final StateNames stateNames;
  private final String windowScope;

  private transient ValueState<A> accumulatorState;
  private transient ValueState<Long> emitTimerState;
  private transient ValueState<Long> processingGapTimerState;
  private transient ValueState<Long> processingMaxLifeTimerState;
  private transient ValueState<Boolean> dirtyState;
  private transient ValueState<Boolean> activeKeyCountedState;
  private transient MapState<String, Boolean> processedEventIdentitiesState;
  private transient ValueState<Long> cleanupTimerState;
  private transient ValueState<Long> scheduledTimerState;
  private transient ValueState<Long> cleanedWindowUntilState;
  private transient Counter duplicateEventsCounter;
  private transient Counter missingEventIdentityCounter;
  private transient Counter closedWindowCorrectionsCounter;
  private transient Counter expiredWindowLateEventsCounter;
  private transient ActiveKeysMetric activeKeysMetric;

  AbstractProcessingTimeWindowFunction(
      long emitMs,
      long gapMs,
      long maxLifeMs,
      long stateTtlMs,
      TypeInformation<A> accumulatorType,
      StateNames stateNames,
      String windowScope) {
    if (emitMs <= 0L || gapMs <= 0L || maxLifeMs <= 0L || stateTtlMs <= 0L) {
      throw new IllegalArgumentException(
          "window timer and correction retention durations must be positive");
    }
    this.emitMs = emitMs;
    this.gapMs = gapMs;
    this.maxLifeMs = maxLifeMs;
    this.stateTtlMs = stateTtlMs;
    this.accumulatorType = accumulatorType;
    this.stateNames = stateNames;
    this.windowScope = windowScope;
  }

  /** 为不同窗口保留已有的 Flink state descriptor 名称。 */
  static final class StateNames implements Serializable {
    private static final long serialVersionUID = 1L;

    private final String accumulator;
    private final String emitTimer;
    private final String processingGapTimer;
    private final String processingMaxLifeTimer;
    private final String dirty;
    private final String activeKeyCounted;

    StateNames(
        String accumulator,
        String emitTimer,
        String processingGapTimer,
        String processingMaxLifeTimer,
        String dirty,
        String activeKeyCounted) {
      this.accumulator = accumulator;
      this.emitTimer = emitTimer;
      this.processingGapTimer = processingGapTimer;
      this.processingMaxLifeTimer = processingMaxLifeTimer;
      this.dirty = dirty;
      this.activeKeyCounted = activeKeyCounted;
    }
  }

  protected abstract A createAccumulator(E event);

  protected abstract void addEvent(A accumulator, E event) throws Exception;

  protected abstract boolean isClosed(A accumulator);

  protected abstract void markClosed(A accumulator);

  /** 业务结束事件可要求立即关闭，而不再等待 processing-time gap/max-life Timer。 */
  protected boolean shouldCloseImmediately(A accumulator) {
    return false;
  }

  protected abstract O toIncrementalSnapshot(A accumulator, long timestamp);

  protected abstract O toFinalSnapshot(A accumulator, long timestamp, String closeReason);

  /** 返回用于故障重放去重的稳定事件标识；缺失时事件不会进入聚合。 */
  @Nullable
  protected abstract String eventIdentity(E event);

  /** 返回事件时间（毫秒），仅在有清理标记时识别落后于 watermark 的事件。 */
  protected abstract long eventTimestampMillis(E event);

  /** 子类可把无法聚合或用于关闭窗口修正的事件输出到审计流。 */
  @Nullable
  protected OutputTag<E> lateEventOutputTag() {
    return null;
  }

  /** 子类可初始化不属于通用窗口生命周期的 keyed state。 */
  protected void openAdditionalState() throws Exception {}

  /** 窗口 cleanup 或重开时清理子类 keyed state。 */
  protected void clearAdditionalState() throws Exception {}

  @Override
  public final void open(OpenContext openContext) throws Exception {
    ValueStateDescriptor<A> accumulatorDescriptor =
        new ValueStateDescriptor<>(stateNames.accumulator, accumulatorType);
    // Independent expiration would hide the accumulator while the deduplication map still
    // contains its IDs, so all window state is cleared by one explicit cleanup timer.
    accumulatorState = getRuntimeContext().getState(accumulatorDescriptor);
    emitTimerState =
        getRuntimeContext()
            .getState(new ValueStateDescriptor<>(stateNames.emitTimer, Long.class));
    processingGapTimerState =
        getRuntimeContext()
            .getState(new ValueStateDescriptor<>(stateNames.processingGapTimer, Long.class));
    processingMaxLifeTimerState =
        getRuntimeContext()
            .getState(
                new ValueStateDescriptor<>(stateNames.processingMaxLifeTimer, Long.class));
    dirtyState =
        getRuntimeContext().getState(new ValueStateDescriptor<>(stateNames.dirty, Boolean.class));
    activeKeyCountedState =
        getRuntimeContext()
            .getState(new ValueStateDescriptor<>(stateNames.activeKeyCounted, Boolean.class));
    processedEventIdentitiesState =
        getRuntimeContext()
            .getMapState(
                new MapStateDescriptor<>(
                    windowScope + "-event-identities", String.class, Boolean.class));
    cleanupTimerState =
        getRuntimeContext()
            .getState(
                new ValueStateDescriptor<>(windowScope + "-state-cleanup-timer", Long.class));
    scheduledTimerState =
        getRuntimeContext()
            .getState(new ValueStateDescriptor<>(windowScope + "-scheduled-timer", Long.class));
    cleanedWindowUntilState =
        getRuntimeContext()
            .getState(new ValueStateDescriptor<>(windowScope + "-cleaned-until", Long.class));
    duplicateEventsCounter = getRuntimeContext().getMetricGroup().counter("duplicate_events");
    missingEventIdentityCounter =
        getRuntimeContext().getMetricGroup().counter("missing_event_identity");
    closedWindowCorrectionsCounter =
        getRuntimeContext().getMetricGroup().counter("closed_window_corrections");
    expiredWindowLateEventsCounter =
        getRuntimeContext().getMetricGroup().counter("expired_window_late_events");
    activeKeysMetric.register(getRuntimeContext().getMetricGroup());
    openAdditionalState();
  }

  @Override
  public final void initializeState(FunctionInitializationContext context) throws Exception {
    activeKeysMetric = ActiveKeysMetric.restore(context, getRuntimeContext(), windowScope);
  }

  @Override
  public final void snapshotState(FunctionSnapshotContext context) throws Exception {
    activeKeysMetric.snapshot();
  }

  long activeKeys() {
    return activeKeysMetric.getValue();
  }

  @Override
  public final void processElement(E event, Context context, Collector<O> out) throws Exception {
    String identity = eventIdentity(event);
    if (identity == null) {
      missingEventIdentityCounter.inc();
      outputLateEvent(context, event);
      return;
    }
    Boolean processed = processedEventIdentitiesState.get(identity);
    if (Boolean.TRUE.equals(processed)) {
      duplicateEventsCounter.inc();
      return;
    }

    A accumulator = accumulatorState.value();
    if (accumulator != null && isClosed(accumulator)) {
      addEvent(accumulator, event);
      processedEventIdentitiesState.put(identity, Boolean.TRUE);
      accumulatorState.update(accumulator);
      long correctionTime = context.timerService().currentProcessingTime();
      out.collect(toFinalSnapshot(accumulator, correctionTime, "late_correction"));
      closedWindowCorrectionsCounter.inc();
      outputLateEvent(context, event);
      return;
    }

    if (accumulator == null
        && cleanedWindowUntilState.value() != null
        && context.timerService().currentWatermark() != Long.MIN_VALUE
        && eventTimestampMillis(event) <= context.timerService().currentWatermark()) {
      expiredWindowLateEventsCounter.inc();
      outputLateEvent(context, event);
      return;
    }

    countActiveKey(context.getCurrentKey());
    long processingTime = context.timerService().currentProcessingTime();
    if (accumulator == null) {
      clearAuxiliaryState();
      clearWindowState();
      cleanedWindowUntilState.clear();
      accumulator = createAccumulator(event);
      long maxLifeTimer = TimeUtils.saturatingAdd(processingTime, maxLifeMs);
      processingMaxLifeTimerState.update(maxLifeTimer);
      if (LOG.isDebugEnabled()) {
        LOG.debug(
            "{} window opened: processingTime={}, gapMs={}, maxLifeMs={}, maxLifeTimer={}, stateTtlMs={}",
            windowScope,
            processingTime,
            gapMs,
            maxLifeMs,
            maxLifeTimer,
            stateTtlMs);
      }
    }

    addEvent(accumulator, event);
    processedEventIdentitiesState.put(identity, Boolean.TRUE);
    accumulatorState.update(accumulator);
    dirtyState.update(Boolean.TRUE);

    if (shouldCloseImmediately(accumulator)) {
      emitFinalAndClose(
          accumulator,
          "normal",
          processingTime,
          context.getCurrentKey(),
          out);
      rescheduleTimer(context.timerService());
      return;
    }

    Long previousGap = processingGapTimerState.value();
    long nextGap = TimeUtils.saturatingAdd(processingTime, gapMs);
    if (previousGap == null || previousGap.longValue() != nextGap) {
      processingGapTimerState.update(nextGap);
    }

    if (emitTimerState.value() == null) {
      long emitTimer = TimeUtils.saturatingAdd(processingTime, emitMs);
      emitTimerState.update(emitTimer);
    }
    rescheduleTimer(context.timerService());
  }

  @Override
  public final void onTimer(long timestamp, OnTimerContext context, Collector<O> out)
      throws Exception {
    if (context.timeDomain() != TimeDomain.PROCESSING_TIME) {
      return;
    }
    Long scheduled = scheduledTimerState.value();
    if (scheduled != null && timestamp != scheduled) {
      return;
    }
    scheduledTimerState.clear();
    Long expectedCleanup = cleanupTimerState.value();
    if (expectedCleanup != null && timestamp >= expectedCleanup) {
      removeActiveKey(context.getCurrentKey());
      clearAuxiliaryState();
      accumulatorState.clear();
      clearWindowState();
      // Retain only a bounded marker after releasing the accumulator and per-event identities.
      cleanedWindowUntilState.update(TimeUtils.saturatingAdd(expectedCleanup, stateTtlMs));
      rescheduleTimer(context.timerService());
      return;
    }

    Long cleanedUntil = cleanedWindowUntilState.value();
    if (cleanedUntil != null && timestamp >= cleanedUntil) {
      cleanedWindowUntilState.clear();
    }

    A accumulator = accumulatorState.value();
    if (accumulator == null) {
      removeActiveKey(context.getCurrentKey());
      clearAuxiliaryState();
      clearWindowState();
      rescheduleTimer(context.timerService());
      return;
    }
    Long expectedMaxLife = processingMaxLifeTimerState.value();
    if (expectedMaxLife != null && timestamp >= expectedMaxLife) {
      emitFinalAndClose(
          accumulator,
          "max_life",
          timestamp,
          context.getCurrentKey(),
          out);
      rescheduleTimer(context.timerService());
      return;
    }

    Long expectedGap = processingGapTimerState.value();
    if (expectedGap != null && timestamp >= expectedGap) {
      emitFinalAndClose(
          accumulator,
          "idle_timeout",
          timestamp,
          context.getCurrentKey(),
          out);
      rescheduleTimer(context.timerService());
      return;
    }

    Long expectedEmit = emitTimerState.value();
    if (expectedEmit == null || timestamp < expectedEmit) {
      rescheduleTimer(context.timerService());
      return;
    }
    if (Boolean.TRUE.equals(dirtyState.value())) {
      out.collect(toIncrementalSnapshot(accumulator, timestamp));
      dirtyState.update(Boolean.FALSE);
      long nextEmit =
          TimeUtils.saturatingAdd(context.timerService().currentProcessingTime(), emitMs);
      emitTimerState.update(nextEmit);
      rescheduleTimer(context.timerService());
      return;
    }
    LOG.debug("skip unchanged {} incremental emit: key={}", windowScope, context.getCurrentKey());
    emitTimerState.clear();
    rescheduleTimer(context.timerService());
  }

  private void emitFinalAndClose(
      A accumulator,
      String reason,
      long timestamp,
      K key,
      Collector<O> out)
      throws Exception {
    out.collect(toFinalSnapshot(accumulator, timestamp, reason));
    markClosed(accumulator);
    accumulatorState.update(accumulator);
    removeActiveKey(key);
    LOG.debug(
        "{} window closed: key={}, reason={}, processingTime={}",
        windowScope,
        key,
        reason,
        timestamp);
    clearAuxiliaryState();
    long cleanupTimer = TimeUtils.saturatingAdd(timestamp, stateTtlMs);
    cleanupTimerState.update(cleanupTimer);
  }

  private void countActiveKey(K key) throws Exception {
    if (Boolean.TRUE.equals(activeKeyCountedState.value())) {
      return;
    }
    activeKeysMetric.keyOpened(String.valueOf(key));
    activeKeyCountedState.update(Boolean.TRUE);
  }

  private void removeActiveKey(K key) throws Exception {
    if (!Boolean.TRUE.equals(activeKeyCountedState.value())) {
      return;
    }
    activeKeysMetric.keyClosed(String.valueOf(key));
    activeKeyCountedState.clear();
  }

  private void clearAuxiliaryState() throws Exception {
    emitTimerState.clear();
    processingGapTimerState.clear();
    processingMaxLifeTimerState.clear();
    dirtyState.clear();
  }

  private void clearWindowState() throws Exception {
    cleanupTimerState.clear();
    processedEventIdentitiesState.clear();
    clearAdditionalState();
  }

  private void rescheduleTimer(TimerService timerService) throws Exception {
    Long next = earlier(emitTimerState.value(), processingGapTimerState.value());
    next = earlier(next, processingMaxLifeTimerState.value());
    next = earlier(next, cleanupTimerState.value());
    next = earlier(next, cleanedWindowUntilState.value());
    Long previous = scheduledTimerState.value();
    if (Objects.equals(previous, next)) {
      return;
    }
    // Flink deduplicates timers by key and timestamp, so only this scheduler may delete them.
    if (previous != null) {
      timerService.deleteProcessingTimeTimer(previous);
    }
    if (next == null) {
      scheduledTimerState.clear();
      return;
    }
    timerService.registerProcessingTimeTimer(next);
    scheduledTimerState.update(next);
  }

  @Nullable
  private static Long earlier(@Nullable Long first, @Nullable Long second) {
    if (first == null) {
      return second;
    }
    return second == null || first <= second ? first : second;
  }

  private void outputLateEvent(Context context, E event) {
    OutputTag<E> outputTag = lateEventOutputTag();
    if (outputTag != null) {
      context.output(outputTag, event);
    }
  }
}
