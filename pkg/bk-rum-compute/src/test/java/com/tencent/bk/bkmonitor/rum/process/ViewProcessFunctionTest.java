// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.process;

import static org.assertj.core.api.Assertions.assertThat;

import com.tencent.bk.bkmonitor.rum.RumJobConfig;
import com.tencent.bk.bkmonitor.rum.model.*;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Function;
import java.util.stream.Collectors;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.streaming.util.OneInputStreamOperatorTestHarness;
import org.apache.flink.streaming.util.ProcessFunctionTestHarnesses;
import org.apache.flink.util.ParameterTool;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * {@link ViewProcessFunction} 单测:点击/资源/错误计数、30s(处理时间)增量 emit、 处理时间 gap 关闭、maxLife 处理时间强制关闭、滑动 gap
 * 续期、hasViewId 过滤。
 *
 * <p>用小间隔(emit=100ms, gap=10s, maxLife 视用例)驱动;{@code setProcessingTime} 推进处理时间 触发 emit / gap /
 * maxLife timer。
 */
class ViewProcessFunctionTest {

  private static final long EMIT_MS = 100L;
  private static final long GAP_MS = 10_000L;
  private static final long MAX_LIFE_MS = 100_000L;
  private static final long STATE_TTL_MS = 200_000L;

  /** 构造一条带 view.id 的 span;span_type / span_name / status.code 按需填充。 */
  private RumEvent span(
      String viewId, String spanType, String spanName, Integer statusCode, long eventTime) {
    Map<String, Object> attrs = new HashMap<>();
    attrs.put("view.id", viewId);
    if (spanType != null) {
      attrs.put("span_type", spanType);
    }
    Span spans = new Span();
    spans.setSpanName(spanName);
    spans.setAttributes(attrs);
    spans.setAppName("ot-demo");
    spans.setBkBizId(2);
    spans.setTraceId("trace-" + viewId);
    spans.setSpanId(spanName + ":" + eventTime);
    if (statusCode != null) {
      SpanStatus spanStatus = new SpanStatus();
      spanStatus.setCode(statusCode);
      spans.setStatus(spanStatus);
    }
    RumEvent event = new RumEvent();
    event.setSpan(spans);
    event.setSpanType(spanType);
    event.setEventTime(eventTime);
    event.setEndTime(eventTime);
    event.setSessionId("session-1");
    return event;
  }

  /** 允许传入额外属性的 span 构造器，用于测试 view.phase / view.end_reason 等路径。 */
  private RumEvent spanWithAttrs(
      String viewId, String spanType, long eventTime, Map<String, Object> extraAttrs) {
    Map<String, Object> attrs = new HashMap<>(extraAttrs);
    attrs.put("view.id", viewId);
    attrs.put("span_type", spanType);
    Span spans = new Span();
    spans.setSpanName(spanType);
    spans.setAttributes(attrs);
    spans.setAppName("ot-demo");
    spans.setBkBizId(2);
    spans.setTraceId("trace-" + viewId);
    spans.setSpanId(spanType + ":" + eventTime);
    RumEvent event = new RumEvent();
    event.setSpan(spans);
    event.setSpanType(spanType);
    event.setEventTime(eventTime);
    event.setEndTime(eventTime);
    event.setSessionId("session-1");
    return event;
  }

  private OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> openHarness(
      long emit, long gap, long maxLife) throws Exception {
    OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        ProcessFunctionTestHarnesses.forKeyedProcessFunction(
            new ViewProcessFunction(emit, gap, maxLife, STATE_TTL_MS),
            ViewProcessFunction::viewKeyOf,
            TypeInformation.of(String.class));
    harness.open();
    return harness;
  }

  private long activeKeys(
      OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness) {
    ViewProcessFunction function =
        (ViewProcessFunction)
            ((org.apache.flink.streaming.api.operators.KeyedProcessOperator<
                        String, RumEvent, ViewEventDocument>)
                    harness.getOneInputOperator())
                .getUserFunction();
    return function.activeKeys();
  }

  @Test
  void defaultWindowRemainsActiveBeyondThreeMinutesWhileEventsContinue() throws Exception {
    RumJobConfig.WindowConfig window =
        RumJobConfig.from(
            ParameterTool.fromMap(
                Map.of("kafka.bootstrap.servers", "localhost:9092", "kafka.topic", "rum-events")))
            .viewWindow();
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(window.emitMs(), window.gapMs(), window.maxLifeMs())) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000L), 1L);
      harness.setProcessingTime(60_000L);
      harness.processElement(span("v1", "click", "second", 0, 2_000L), 2L);

      harness.setProcessingTime(180_001L);

      assertThat(harness.extractOutputValues()).isNotEmpty()
          .allSatisfy(snapshot -> assertThat(snapshot.isClosed()).isFalse());
      assertThat(activeKeys(harness)).isEqualTo(1L);
    }
  }

  @ParameterizedTest
  @CsvSource({"100,1000,idle_timeout", "1000,100,max_life"})
  void correctionsKeepInitialCloseReason(long gapMs, long maxLifeMs, String reason) throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, gapMs, maxLifeMs)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000L), 1L);
      harness.setProcessingTime(100L);
      ViewEventDocument closed = harness.extractOutputValues().get(0);
      assertThat(closed.getCloseReason()).isEqualTo(reason);

      harness.processElement(
          spanWithAttrs("v1", "view", 2_000L, Map.of("view.phase", "end")), 2L);

      ViewEventDocument corrected = harness.extractOutputValues().get(1);
      assertThat(corrected.getCloseReason()).isEqualTo(reason);
      assertThat(corrected.getClosTim()).isEqualTo(closed.getClosTim());
      assertThat(corrected.getWindowId()).isEqualTo(closed.getWindowId());
    }
  }

  @Test
  void gapRenewalKeepsCoincidentEmitDeadline() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, 100L, 1_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000L), 1L);
      harness.setProcessingTime(50L);
      harness.processElement(span("v1", "click", "second", 0, 2_000L), 2L);

      harness.setProcessingTime(100L);

      assertThat(harness.extractOutputValues()).hasSize(1);
      assertThat(harness.extractOutputValues().get(0).isClosed()).isFalse();
      assertThat(harness.extractOutputValues().get(0).getActionCount()).isEqualTo(2L);
      assertThat(harness.numProcessingTimeTimers()).isEqualTo(1);
      harness.setProcessingTime(150L);
      assertThat(harness.extractOutputValues()).hasSize(2);
      assertThat(harness.extractOutputValues().get(1).isClosed()).isTrue();
    }
  }

  @ParameterizedTest
  @ValueSource(longs = {1_000L, 10_000L})
  void gapRenewalKeepsCoincidentMaxLifeDeadline(long emitMs) throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(emitMs, 1_000L, 1_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000L), 1L);
      harness.setProcessingTime(50L);
      harness.processElement(span("v1", "click", "second", 0, 2_000L), 2L);

      harness.setProcessingTime(1_000L);

      assertThat(harness.extractOutputValues()).hasSize(1);
      assertThat(harness.extractOutputValues().get(0).isClosed()).isTrue();
      assertThat(harness.extractOutputValues().get(0).getActionCount()).isEqualTo(2L);
      assertThat(harness.numProcessingTimeTimers()).isEqualTo(1);
      harness.setProcessingTime(1_050L);
      assertThat(harness.extractOutputValues()).hasSize(1);
    }
  }

  @Test
  void coincidentDeadlinesEmitOnlyOneFinalSnapshot() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, 100L, 100L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000L), 1L);

      harness.setProcessingTime(100L);

      assertThat(harness.extractOutputValues()).hasSize(1);
      assertThat(harness.extractOutputValues().get(0).isClosed()).isTrue();
      assertThat(activeKeys(harness)).isZero();
      assertThat(harness.numProcessingTimeTimers()).isEqualTo(1);
    }
  }

  @Test
  void firstViewBehindWatermarkStillOpensWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processWatermark(5_000L);

      harness.processElement(span("first-view", "click", "late", 0, 1_000_000L), 1_000L);
      harness.setProcessingTime(EMIT_MS);

      assertThat(harness.extractOutputValues()).hasSize(1);
      assertThat(harness.extractOutputValues().get(0).getActionCount()).isEqualTo(1L);
      assertThat(harness.extractOutputValues().get(0).getWindowId()).isNotBlank();
      assertThat(activeKeys(harness)).isEqualTo(1L);
    }
  }

  @Test
  void expiredCleanupMarkerAllowsLateEventToOpenNewWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000_000L), 1_000L);
      harness.setProcessingTime(1_000L);
      String windowId = harness.extractOutputValues().get(0).getWindowId();
      harness.processWatermark(5_000L);
      harness.setProcessingTime(STATE_TTL_MS + 1_000L);
      harness.processElement(span("v1", "click", "expired", 0, 2_000_000L), 2_000L);
      assertThat(harness.extractOutputValues()).hasSize(1);

      harness.setProcessingTime(2L * STATE_TTL_MS + 1_000L);
      assertThat(harness.numProcessingTimeTimers()).isZero();
      harness.processElement(span("v1", "click", "reopened", 0, 3_000_000L), 3_000L);
      harness.setProcessingTime(2L * STATE_TTL_MS + 2_000L);

      assertThat(harness.extractOutputValues()).hasSize(2);
      ViewEventDocument reopened = harness.extractOutputValues().get(1);
      assertThat(reopened.getWindowId()).isNotBlank().isNotEqualTo(windowId);
      assertThat(reopened.getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void restoredCleanupMarkerDistinguishesFirstArrivalFromExpiredWindow() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> first =
        openHarness(EMIT_MS, 1_000L, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(span("known", "click", "first", 0, 1_000_000L), 1_000L);
      first.setProcessingTime(STATE_TTL_MS + 1_001L);
      snapshot = first.snapshot(1L, 1L);
    }
    ViewProcessFunction function =
        new ViewProcessFunction(EMIT_MS, 1_000L, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, ViewEventDocument> restored =
        new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
            new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
            ViewProcessFunction::viewKeyOf, TypeInformation.of(String.class), 1, 1, 0)) {
      restored.initializeState(snapshot);
      restored.open();
      restored.setProcessingTime(STATE_TTL_MS + 1_001L);
      restored.processWatermark(5_000L);

      restored.processElement(span("known", "click", "late", 0, 2_000_000L), 2_000L);
      restored.processElement(span("first-seen", "click", "late", 0, 2_000_000L), 2_000L);
      restored.setProcessingTime(STATE_TTL_MS + 1_001L + EMIT_MS);

      assertThat(restored.extractOutputValues()).hasSize(1);
      assertThat(restored.extractOutputValues().get(0).getViewId()).isEqualTo("first-seen");
      assertThat(restored.extractOutputValues().get(0).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void shortCorrectionRetentionStartsOnlyAfterWindowCloses() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        ProcessFunctionTestHarnesses.forKeyedProcessFunction(
            new ViewProcessFunction(10_000L, 1_000L, 500L, 50L),
            ViewProcessFunction::viewKeyOf, TypeInformation.of(String.class))) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first", 0, 1_000L), 1L);
      harness.setProcessingTime(450L);
      harness.processElement(span("v1", "click", "second", 0, 2_000L), 2L);
      assertThat(harness.extractOutputValues()).isEmpty();

      harness.setProcessingTime(500L);
      harness.setProcessingTime(549L);
      harness.processElement(span("v1", "click", "correction", 0, 3_000L), 3L);

      assertThat(harness.extractOutputValues()).hasSize(2);
      assertThat(harness.extractOutputValues().get(1).getActionCount()).isEqualTo(3L);
      assertThat(harness.extractOutputValues().get(1).getCloseReason()).isEqualTo("max_life");
      harness.setProcessingTime(550L);
      assertThat(harness.numProcessingTimeTimers()).isEqualTo(1);
      harness.setProcessingTime(600L);
      assertThat(harness.numProcessingTimeTimers()).isZero();
    }
  }

  @Test
  void restoreProcessesOverdueCloseCleanupAndMarkerExpiry() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> first =
        openHarness(EMIT_MS, 1_000L, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(span("v1", "click", "first", 0, 1_000_000L), 1_000L);
      snapshot = first.snapshot(1L, 1L);
    }
    ViewProcessFunction function =
        new ViewProcessFunction(EMIT_MS, 1_000L, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, ViewEventDocument> restored =
        new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
            new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
            ViewProcessFunction::viewKeyOf, TypeInformation.of(String.class), 1, 1, 0)) {
      restored.initializeState(snapshot);
      restored.open();

      restored.setProcessingTime(2L * STATE_TTL_MS + 1_001L);

      assertThat(restored.extractOutputValues()).hasSize(2);
      ViewEventDocument closed = restored.extractOutputValues().get(1);
      assertThat(closed.isClosed()).isTrue();
      assertThat(closed.getCloseReason()).isEqualTo("idle_timeout");
      assertThat(restored.numProcessingTimeTimers()).isZero();
      assertThat(function.activeKeys()).isZero();
      restored.processWatermark(5_000L);
      restored.processElement(span("v1", "click", "reopened", 0, 2_000_000L), 2_000L);
      restored.setProcessingTime(2L * STATE_TTL_MS + 1_001L + EMIT_MS);
      assertThat(restored.extractOutputValues()).hasSize(3);
      assertThat(restored.extractOutputValues().get(2).getWindowId())
          .isNotBlank().isNotEqualTo(closed.getWindowId());
    }
  }

  @Test
  void isolatesSameViewIdAcrossBusinessesAndApps() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      for (int i = 0; i < 3; i++) {
        RumEvent event = span("shared-view", "click", "click", 0, 1_000L);
        event.getSpan().setBkBizId(i == 1 ? 2 : 1);
        event.getSpan().setAppName(i == 2 ? "app-b" : "app-a");
        harness.processElement(event, 1_000L);
        harness.processElement(event, 1_000L);
      }
      harness.setProcessingTime(EMIT_MS + 1L);

      List<ViewEventDocument> output = harness.extractOutputValues();
      assertThat(output).hasSize(3);
      assertThat(output).extracting(event -> event.getBkBizId() + ":" + event.getAppName())
          .containsExactlyInAnyOrder("1:app-a", "2:app-a", "1:app-b");
      assertThat(output).allSatisfy(event -> {
        assertThat(event.getViewId()).isEqualTo("shared-view");
        assertThat(event.getActionCount()).isEqualTo(1L);
      });
    }
  }

  @Test
  void missingTenantDimensionsDoNotOpenWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      RumEvent missingBusiness = span("view", "click", "missing-business", 0, 1_000L);
      missingBusiness.getSpan().setBkBizId(null);
      RumEvent missingApp = span("view", "click", "missing-app", 0, 1_000L);
      missingApp.getSpan().setAppName(null);

      harness.processElement(missingBusiness, 1_000L);
      harness.processElement(missingApp, 1_000L);

      assertThat(harness.extractOutputValues()).isEmpty();
      assertThat(activeKeys(harness)).isZero();
    }
  }

  @Test
  void restoredOverdueTimerDeduplicatesReplayAndCorrectsClosedView() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    String windowId;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> first =
        openHarness(EMIT_MS, 1_000L, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(span("v1", "click", "before-checkpoint", 0, 1_000L), 1_000L);
      snapshot = first.snapshot(1L, 1L);
      first.setProcessingTime(EMIT_MS + 1L);
      windowId = first.extractOutputValues().get(0).getWindowId();
      assertThat(windowId).isNotBlank();
    }

    ViewProcessFunction function =
        new ViewProcessFunction(EMIT_MS, 1_000L, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, ViewEventDocument>
        restored =
            new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
                new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
                ViewProcessFunction::viewKeyOf,
                TypeInformation.of(String.class),
                1,
                1,
                0)) {
      restored.initializeState(snapshot);
      restored.open();
      restored.setProcessingTime(1_001L);
      assertThat(restored.extractOutputValues()).hasSize(2);

      restored.processElement(span("v1", "click", "before-checkpoint", 0, 1_000L), 1_000L);
      assertThat(restored.extractOutputValues()).hasSize(2);
      restored.processElement(span("v1", "click", "after-checkpoint", 0, 2_000L), 2_000L);

      List<ViewEventDocument> output = restored.extractOutputValues();
      assertThat(output).hasSize(3);
      assertThat(output.get(2).isClosed()).isTrue();
      assertThat(output.get(2).getActionCount()).isEqualTo(2L);
      assertThat(output.get(2).getClosTim()).isEqualTo(output.get(1).getClosTim());
      assertThat(output).extracting(ViewEventDocument::getWindowId).containsOnly(windowId);
    }
  }

  @Test
  void duplicateViewIdentityIsIgnored() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1_000L), 1_000L);
      harness.processElement(span("v1", "click", "click", 0, 1_000L), 1_000L);
      harness.setProcessingTime(101L);

      assertThat(harness.extractOutputValues()).hasSize(1);
      assertThat(harness.extractOutputValues().get(0).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void activeKeysTracksOpenAndClosedViews() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      assertThat(activeKeys(harness)).isEqualTo(0L);

      harness.processElement(span("v1", "click", "first.click", 0, 1_000L), 1_000L);
      harness.processElement(span("v1", "click", "second.click", 0, 1_100L), 1_100L);
      harness.processElement(span("v2", "click", "third.click", 0, 1_200L), 1_200L);
      assertThat(activeKeys(harness)).isEqualTo(2L);

      harness.setProcessingTime(1_001L);
      assertThat(activeKeys(harness)).isEqualTo(0L);
    }
  }

  @Test
  void activeKeysIsRestoredFromCheckpoint() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> first =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(span("v1", "click", "first.click", 0, 1_000L), 1_000L);
      first.processElement(span("v2", "click", "second.click", 0, 1_100L), 1_100L);
      snapshot = first.snapshot(1L, 1L);
    }

    ViewProcessFunction function =
        new ViewProcessFunction(10_000L, 1_000L, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, ViewEventDocument>
        restored =
            new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
                new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
                ViewProcessFunction::viewKeyOf,
                TypeInformation.of(String.class),
                1,
                1,
                0)) {
      restored.initializeState(snapshot);
      restored.open();

      assertThat(function.activeKeys()).isEqualTo(2L);
      restored.setProcessingTime(1_001L);
      assertThat(function.activeKeys()).isEqualTo(0L);
    }
  }

  @Test
  void aggregatesNetworkEffectiveTypeFromFormalAttribute() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      RumEvent first = span("v1", "view", "browser.view", 0, 1000L);
      first.getSpan().getAttributes().put("network.effective_type", "4g");
      RumEvent later = span("v1", "view", "browser.view", 0, 1100L);
      later.getSpan().getAttributes().put("network.effective_type", "3g");
      harness.setProcessingTime(0L);
      harness.processElement(first, 1000L);
      harness.processElement(later, 1100L);
      harness.setProcessingTime(101L);
      assertThat(harness.extractOutputValues().get(0).getNetworkType()).isEqualTo("4g");
    }
  }
  @Test
  void countsClickResourceErrorAndEmitsIncrementally() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);
      harness.processElement(span("v1", "resource", "script.js", 0, 1100L), 1100L);
      harness.processElement(span("v1", "error", "js_error", 1, 1200L), 1200L);

      harness.setProcessingTime(101L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      ViewEventDocument v = out.get(0);
      assertThat(v.getViewId()).isEqualTo("v1");
      assertThat(v.getActionCount()).isEqualTo(1L);
      assertThat(v.getResourceCount()).isEqualTo(1L);
      assertThat(v.getErrorCount()).isEqualTo(1L);
      assertThat(v.isClosed()).isFalse();
    }
  }


  @Test
  void unchangedStatsAreNotEmittedRepeatedly() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);

      harness.setProcessingTime(101L);
      List<ViewEventDocument> first = harness.extractOutputValues();
      assertThat(first.size()).isEqualTo(1);
      assertThat(first.get(0).isClosed()).isFalse();

      harness.setProcessingTime(202L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);

      harness.processElement(span("v1", "click", "click", 0, 1200L), 1200L);
      harness.setProcessingTime(303L);
      List<ViewEventDocument> restarted = harness.extractOutputValues();
      assertThat(restarted.size()).isEqualTo(2);
      assertThat(restarted.get(1).isClosed()).isFalse();
      assertThat(restarted.get(1).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void unchangedEmitStopsAcrossMultipleIntervalsAndRestartsAfterEvent() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);

      harness.setProcessingTime(101L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);
      harness.setProcessingTime(1_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);
      harness.setProcessingTime(5_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);

      harness.processElement(span("v1", "click", "click", 0, 2000L), 2000L);
      harness.setProcessingTime(5_101L);
      List<ViewEventDocument> restarted = harness.extractOutputValues();
      assertThat(restarted.size()).isEqualTo(2);
      assertThat(restarted.get(1).isClosed()).isFalse();
      assertThat(restarted.get(1).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void nonCountingEventStillExtendsProcessingGap() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);
      harness.setProcessingTime(500L);
      harness.processElement(span("v1", "view", "browser.view", 0, 1100L), 1100L);

      harness.setProcessingTime(1_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(0);
      harness.setProcessingTime(1_501L);
      List<ViewEventDocument> closed = harness.extractOutputValues();
      assertThat(closed.size()).isEqualTo(1);
      assertThat(closed.get(0).isClosed()).isTrue();
      assertThat(closed.get(0).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void sameProcessingTimeEventsShareOneGapTimer() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);
      int timersAfterFirstEvent = harness.numProcessingTimeTimers();
      harness.processElement(span("v1", "click", "click", 0, 1100L), 1100L);
      assertThat(harness.numProcessingTimeTimers()).isEqualTo(timersAfterFirstEvent);

      harness.setProcessingTime(1_001L);
      List<ViewEventDocument> closed = harness.extractOutputValues();
      assertThat(closed.size()).isEqualTo(1);
      assertThat(closed.get(0).isClosed()).isTrue();
      assertThat(closed.get(0).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void closedViewCorrectsUntilCleanupThenAllowsFreshWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, 1_000L, 5_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "first.click", 0, 1_000L), 1_000L);
      harness.setProcessingTime(101L);
      List<ViewEventDocument> incremental = harness.extractOutputValues();
      assertThat(incremental.size()).isEqualTo(1);
      assertThat(incremental.get(0).isClosed()).isFalse();

      String windowId = incremental.get(0).getWindowId();
      assertThat(windowId).isNotBlank();

      harness.setProcessingTime(1_001L);
      List<ViewEventDocument> closed = harness.extractOutputValues();
      assertThat(closed.size()).isEqualTo(2);
      assertThat(closed.get(1).isClosed()).isTrue();

      harness.processElement(span("v1", "click", "corrected.click", 0, 2_000L), 2_000L);
      List<ViewEventDocument> corrected = harness.extractOutputValues();
      assertThat(corrected).hasSize(3);
      assertThat(corrected.get(2).isClosed()).isTrue();
      assertThat(corrected.get(2).getActionCount()).isEqualTo(2L);
      assertThat(corrected.get(2).getClosTim()).isEqualTo(closed.get(1).getClosTim());
      assertThat(corrected).extracting(ViewEventDocument::getWindowId).containsOnly(windowId);

      harness.setProcessingTime(STATE_TTL_MS + 1_002L);
      harness.processElement(span("v1", "click", "reopened.click", 0, 3_000L), 3_000L);
      harness.setProcessingTime(STATE_TTL_MS + 1_103L);
      List<ViewEventDocument> reopened = harness.extractOutputValues();
      assertThat(reopened.size()).isEqualTo(4);
      assertThat(reopened.get(3).isClosed()).isFalse();
      assertThat(reopened.get(3).getActionCount()).isEqualTo(1L);
      assertThat(reopened.get(3).getViewId()).isEqualTo(incremental.get(0).getViewId());
      assertThat(reopened.get(3).getWindowId()).isNotBlank().isNotEqualTo(windowId);
    }
  }

  @Test
  void processingGapCloseEmitsFinalAndAcceptsCorrection() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L); // processing gap=10000
      harness.setProcessingTime(101L); // 增量 emit
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);

      // 推进处理时间越过 gap -> 最终汇总; closed 状态仍保留在 TTL 内
      harness.setProcessingTime(10_001L);
      List<ViewEventDocument> closed = harness.extractOutputValues();
      assertThat(closed.size()).isEqualTo(2);
      assertThat(closed.get(1).isClosed()).isTrue();

      harness.processElement(span("v1", "click", "corrected.click", 0, 20000L), 20000L);
      List<ViewEventDocument> corrected = harness.extractOutputValues();
      assertThat(corrected).hasSize(3);
      assertThat(corrected.get(2).isClosed()).isTrue();
      assertThat(corrected.get(2).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void explicitCleanupTimerAllowsReopen() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, 1_000L, 5_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1_000L), 1_000L);
      harness.setProcessingTime(10_001L);
      List<ViewEventDocument> closed = harness.extractOutputValues();
      assertThat(closed.size()).isEqualTo(2);
      assertThat(closed.get(1).isClosed()).isTrue();

      harness.processElement(span("v1", "click", "corrected.click", 0, 2_000L), 2_000L);
      assertThat(harness.extractOutputValues()).hasSize(3);
      harness.setProcessingTime(STATE_TTL_MS + 10_002L);
      harness.processElement(span("v1", "click", "reopened.click", 0, 3_000L), 3_000L);
      harness.setProcessingTime(STATE_TTL_MS + 10_103L);
      List<ViewEventDocument> reopened = harness.extractOutputValues();
      assertThat(reopened.size()).isEqualTo(4);
      assertThat(reopened.get(3).isClosed()).isFalse();
      assertThat(reopened.get(3).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void processingGapClosesOnProcessingTime() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);

      harness.setProcessingTime(101L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);

      harness.setProcessingTime(1_001L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(2);
      assertThat(out.get(1).isClosed()).isTrue();
      assertThat(out.get(1).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void processingGapIsExtendedByNewEvent() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);

      harness.setProcessingTime(500L);
      harness.processElement(span("v1", "click", "click", 0, 1200L), 1200L);

      harness.setProcessingTime(1_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(0);

      harness.setProcessingTime(1_501L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      assertThat(out.get(0).isClosed()).isTrue();
      assertThat(out.get(0).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void maxLifeClose() throws Exception {
    // gap 远大于 maxLife,确保是 maxLife 而非 gap 触发关闭。
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 100_000L, 5_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(
          span("v1", "click", "click", 0, 1000L), 1000L); // gap=100000, maxLife=5000

      harness.setProcessingTime(5_001L); // 越过 maxLife,未到 gap
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      assertThat(out.get(0).isClosed()).isTrue();
      assertThat(out.get(0).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void processingMaxLifeClosesOnProcessingTime() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 100_000L, 1_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);

      harness.setProcessingTime(1_001L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      assertThat(out.get(0).isClosed()).isTrue();
      assertThat(out.get(0).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void slidingGapExtendsOnNewEvent() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L); // processing gap=10000
      harness.setProcessingTime(9_000L);
      harness.processElement(
          span("v1", "click", "click", 0, 9000L), 9000L); // processing gap 推到 19000

      // 越过旧 gap(10000)但未到新 gap(19000)-> 不关闭。
      harness.setProcessingTime(11_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(0);

      // 越过新 gap -> 关闭,计数为 2
      harness.setProcessingTime(19_001L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      assertThat(out.get(0).isClosed()).isTrue();
      assertThat(out.get(0).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void maxLifeRemainsFixedWhenGapIsExtended() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, GAP_MS, 5_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1_000L), 1_000L);
      harness.setProcessingTime(4_000L);
      harness.processElement(span("v1", "click", "click", 0, 4_000L), 4_000L);

      harness.setProcessingTime(5_001L);
      List<ViewEventDocument> output = harness.extractOutputValues();
      assertThat(output.size()).isEqualTo(1);
      assertThat(output.get(0).isClosed()).isTrue();
      assertThat(output.get(0).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void closingSessionCancelsProcessingEmit() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1_000L), 1_000L);
      harness.setProcessingTime(1_001L);
      int outputCount = harness.extractOutputValues().size();
      assertThat(outputCount).isEqualTo(1);

      harness.setProcessingTime(10_000L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(outputCount);
    }
  }

  @Test
  void outOfOrderEventDoesNotMoveLastEventBackwards() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 9_000L), 9_000L);
      harness.processElement(span("v1", "click", "click", 0, 1_000L), 1_000L);
      harness.setProcessingTime(10_001L);

      List<ViewEventDocument> output = harness.extractOutputValues();
      assertThat(output.size()).isEqualTo(1);
      assertThat(output.get(0).getLastEventTs()).isEqualTo(9_000L);
      assertThat(output.get(0).getActionCount()).isEqualTo(2L);
    }
  }

  @Test
  void hasViewIdFiltersByViewIdPresence() {
    assertThat(ViewProcessFunction.hasViewId(span("v", "click", "click", 0, 1L))).isTrue();

    RumEvent noViewId = new RumEvent();
    Span s = new Span();
    s.setAttributes(new HashMap<>());
    noViewId.setSpan(s);
    assertThat(ViewProcessFunction.hasViewId(noViewId)).isFalse();
  }

  // ---------- keyed state 增量聚合行为 ----------

  @Test
  void batchOfMultipleEventsIsAggregatedOnTimer() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      // 推 10 click + 5 resource + 2 error，增量更新 keyed state，尚未 emit。
      for (int i = 0; i < 10; i++) {
        harness.processElement(span("v1", "click", "click", 0, 1000L + i), 1000L);
      }
      for (int i = 0; i < 5; i++) {
        harness.processElement(span("v1", "resource", "script.js", 0, 2000L + i), 2000L);
      }
      for (int i = 0; i < 2; i++) {
        harness.processElement(span("v1", "error", "js_error", 1, 3000L + i), 3000L);
      }

      // 推进到 emitMs timer 触发；输出一次累计快照。
      harness.setProcessingTime(101L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      ViewEventDocument v = out.get(0);
      assertThat(v.getViewId()).isEqualTo("v1");
      assertThat(v.getActionCount()).isEqualTo(10L);
      assertThat(v.getResourceCount()).isEqualTo(5L);
      assertThat(v.getErrorCount()).isEqualTo(2L);
      // lastEventTs 始终保留 max(1000..3001)，事件时间已是微秒域。
      assertThat(v.getLastEventTs()).isEqualTo(3_001L);
      assertThat(v.isClosed()).isFalse();
    }
  }

  @Test
  void interleavedViewIdsRemainIsolated() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);
      harness.processElement(span("v2", "resource", "script.js", 0, 1100L), 1100L);
      harness.processElement(span("v1", "click", "click", 0, 1200L), 1200L);
      harness.processElement(span("v2", "error", "js_error", 1, 1300L), 1300L);

      harness.setProcessingTime(101L);
      Map<String, ViewEventDocument> byView =
          harness.extractOutputValues().stream()
              .collect(Collectors.toMap(ViewEventDocument::getViewId, Function.identity()));
      assertThat(byView.size()).isEqualTo(2);
      assertThat(byView.get("v1").getActionCount()).isEqualTo(2L);
      assertThat(byView.get("v1").getResourceCount()).isEqualTo(0L);
      assertThat(byView.get("v1").getErrorCount()).isEqualTo(0L);
      assertThat(byView.get("v2").getActionCount()).isEqualTo(0L);
      assertThat(byView.get("v2").getResourceCount()).isEqualTo(1L);
      assertThat(byView.get("v2").getErrorCount()).isEqualTo(1L);
    }
  }

  @Test
  void gapCloseIncludesEventsNotYetEmitted() throws Exception {
    // 推 3 click，不推进到 emit timer；直接越过 gap 关闭窗口，final summary 必须含 3。
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 1_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);
      harness.processElement(span("v1", "click", "click", 0, 1100L), 1100L);
      harness.processElement(span("v1", "click", "click", 0, 1200L), 1200L);

      // 直接越过 gap=1000；不经过 emit timer；final summary 应包含全部 3 条 click
      harness.setProcessingTime(1_001L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      assertThat(out.get(0).isClosed()).isTrue();
      assertThat(out.get(0).getActionCount()).isEqualTo(3L);
    }
  }

  @Test
  void maxLifeCloseIncludesEventsNotYetEmitted() throws Exception {
    // 同上但走 maxLife 路径：gap 很大、maxLife 极小
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(10_000L, 100_000L, 1_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);
      harness.processElement(span("v1", "resource", "script.js", 0, 1100L), 1100L);
      harness.processElement(span("v1", "error", "js_error", 1, 1200L), 1200L);

      // 越过 maxLife=1000 但远未到 gap=100000
      harness.setProcessingTime(1_001L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out.size()).isEqualTo(1);
      assertThat(out.get(0).isClosed()).isTrue();
      assertThat(out.get(0).getActionCount()).isEqualTo(1L);
      assertThat(out.get(0).getResourceCount()).isEqualTo(1L);
      assertThat(out.get(0).getErrorCount()).isEqualTo(1L);
    }
  }

  @Test
  void cleanStateOnEmitTimerCancelsSelfLoop() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "click", "click", 0, 1000L), 1000L);

      // 第一次 emit timer 触发：有事件 -> 输出 + 续注册
      harness.setProcessingTime(101L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);

      // 后续无新事件；连续推进过多个 emitMs 周期，emit timer 应已取消自循环，无新输出
      harness.setProcessingTime(202L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);
      harness.setProcessingTime(1_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);
      harness.setProcessingTime(5_001L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);
    }
  }

  /**
   * closeReason 从 view.phase=end 派生（与 Session 的 session.phase 同源，CSV end_reason 语义）。
   */
  @Test
  void closeReasonDerivedFromViewPhase() throws Exception {
    long gapMs = 5_000L;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, gapMs, 100_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "view", "browser.view", 0, 1_000L), 1_000L);

      // 推进到 gap 触发关闭；没上报过 view.phase -> idle_timeout
      harness.setProcessingTime(gapMs + 1L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      ViewEventDocument closed = out.get(out.size() - 1);
      assertThat(closed.isClosed()).isTrue();
      assertThat(closed.getCloseReason()).isEqualTo("idle_timeout");
      // viewEndReason 字段保持 SDK 原值，不参与 closeReason 派生
      assertThat(closed.getViewEndReason()).isNull();
    }
  }

  /** view.phase=end 上报后立即关闭并输出 closeReason=normal。 */
  @Test
  void viewPhaseEndTriggersNormalCloseReason() throws Exception {
    long gapMs = 5_000L;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, gapMs, 100_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "view", "browser.view", 0, 1_000L), 1_000L);

      Map<String, Object> phaseAttrs = new HashMap<>();
      phaseAttrs.put("view.phase", "end");
      harness.processElement(spanWithAttrs("v1", "view", 2_000L, phaseAttrs), 2_000L);

      List<ViewEventDocument> out = harness.extractOutputValues();
      assertThat(out).hasSize(1);
      ViewEventDocument closed = out.get(0);
      assertThat(closed.isClosed()).isTrue();
      assertThat(closed.isActive()).isFalse();
      assertThat(closed.getCloseReason()).isEqualTo("normal");
      assertThat(activeKeys(harness)).isZero();
    }
  }

  /** 只有 view.end_reason=end 而 view.phase 不为 end 时，仍因空闲关闭。 */
  @Test
  void viewEndReasonAloneDoesNotTriggerNormalCloseReason() throws Exception {
    long gapMs = 5_000L;
    try (OneInputStreamOperatorTestHarness<RumEvent, ViewEventDocument> harness =
        openHarness(100L, gapMs, 100_000L)) {
      harness.setProcessingTime(0L);
      harness.processElement(span("v1", "view", "browser.view", 0, 1_000L), 1_000L);

      Map<String, Object> endReasonAttrs = new HashMap<>();
      endReasonAttrs.put("view.end_reason", "end");
      harness.processElement(
          spanWithAttrs("v1", "view", 2_000L, endReasonAttrs), 2_000L);

      // 推进到 gap 触发关闭；没上过 phase=end -> idle_timeout
      harness.setProcessingTime(gapMs + 1L);
      List<ViewEventDocument> out = harness.extractOutputValues();
      ViewEventDocument closed = out.get(out.size() - 1);
      assertThat(closed.isClosed()).isTrue();
      assertThat(closed.getCloseReason()).isEqualTo("idle_timeout");
      // viewEndReason 字段保留 SDK 原值
      assertThat(closed.getViewEndReason()).isEqualTo("end");
    }
  }
}
