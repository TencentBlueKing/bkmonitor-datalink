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

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.model.Span;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.streaming.util.OneInputStreamOperatorTestHarness;
import org.apache.flink.streaming.util.ProcessFunctionTestHarnesses;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

class SessionizeProcessFunctionTest {
  private static final long EMIT_MS = 100L;
  private static final long GAP_MS = 1_000L;
  private static final long MAX_LIFE_MS = 5_000L;
  private static final long STATE_TTL_MS = 200_000L;

  private OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> openHarness(
      long emitMs, long gapMs, long maxLifeMs) throws Exception {
    OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        ProcessFunctionTestHarnesses.forKeyedProcessFunction(
            new SessionizeProcessFunction(emitMs, gapMs, maxLifeMs, STATE_TTL_MS),
            SessionizeProcessFunction::sessionKeyOf,
            TypeInformation.of(String.class));
    harness.open();
    return harness;
  }

  private RumEvent event(String sessionId, String type, String name, long eventTime) {
    Map<String, Object> attributes = new HashMap<>();
    attributes.put("span_type", type);
    attributes.put("view.url", "https://example.test/" + name);
    Span span = new Span();
    span.setAttributes(attributes);
    span.setSpanName(name);
    span.setAppName("rum-app");
    span.setBkBizId(42);
    span.setTraceId("trace-" + sessionId);
    span.setSpanId(name + ":" + eventTime);

    RumEvent event = new RumEvent();
    event.setSessionId(sessionId);
    event.setEventTime(eventTime);
    event.setEndTime(eventTime);
    event.setSpanType(type);
    event.setSpan(span);
    return event;
  }

  private RumEvent event(String type, String name, long eventTime) {
    return event("session-1", type, name, eventTime);
  }

  private long activeKeys(OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness) {
    SessionizeProcessFunction function =
        (SessionizeProcessFunction)
            ((org.apache.flink.streaming.api.operators.KeyedProcessOperator<
                        String, RumEvent, SessionEvent>)
                    harness.getOneInputOperator())
                .getUserFunction();
    return function.activeKeys();
  }

  @Test
  void restoredClosedSessionKeepsOriginalCloseReasonAfterLateBusinessEnd() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> first =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(event("action", "first", 1_000L), 1L);
      first.setProcessingTime(GAP_MS);
      snapshot = first.snapshot(1L, 1L);
    }
    SessionizeProcessFunction function =
        new SessionizeProcessFunction(10_000L, GAP_MS, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, SessionEvent> restored =
        new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
            new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
            SessionizeProcessFunction::sessionKeyOf, TypeInformation.of(String.class), 1, 1, 0)) {
      restored.initializeState(snapshot);
      restored.open();
      restored.setProcessingTime(GAP_MS + 1L);
      RumEvent end = event("session", "late-end", 2_000L);
      end.getSpan().getAttributes().put("session.phase", "end");

      restored.processElement(end, 2L);

      assertThat(restored.extractOutputValues()).hasSize(1);
      assertThat(restored.extractOutputValues().get(0).getEndReason()).isEqualTo("idle_timeout");
      assertThat(restored.extractOutputValues().get(0).getClosTim()).isEqualTo(GAP_MS * 1_000L);
    }
  }

  @ParameterizedTest
  @CsvSource({"100,1000,idle_timeout", "1000,100,max_life"})
  void correctionsKeepInitialCloseReason(long gapMs, long maxLifeMs, String reason) throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, gapMs, maxLifeMs)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first", 1_000L), 1L);
      harness.setProcessingTime(100L);
      SessionEvent closed = harness.extractOutputValues().get(0);
      assertThat(closed.getEndReason()).isEqualTo(reason);
      RumEvent end = event("session", "late-end", 2_000L);
      end.getSpan().getAttributes().put("session.phase", "end");

      harness.processElement(end, 2L);

      SessionEvent corrected = harness.extractOutputValues().get(1);
      assertThat(corrected.getEndReason()).isEqualTo(reason);
      assertThat(corrected.getClosTim()).isEqualTo(closed.getClosTim());
      assertThat(corrected.getWindowId()).isEqualTo(closed.getWindowId());
    }
  }

  @Test
  void firstSessionBehindWatermarkStillOpensWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processWatermark(5_000L);

      harness.processElement(event("action", "late-first", 1_000_000L), 1_000L);
      harness.setProcessingTime(EMIT_MS);

      assertThat(harness.extractOutputValues()).hasSize(1);
      assertThat(harness.extractOutputValues().get(0).getActionCount()).isEqualTo(1L);
      assertThat(harness.getSideOutput(SessionizeProcessFunction.LATE_EVENTS)).isNull();
      assertThat(activeKeys(harness)).isEqualTo(1L);
    }
  }

  @Test
  void isolatesSameSessionIdAcrossBusinessesAndApps() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      for (int i = 0; i < 3; i++) {
        RumEvent event = event("shared-session", "action", "click", 1_000L);
        event.getSpan().setBkBizId(i == 1 ? 2 : 1);
        event.getSpan().setAppName(i == 2 ? "app-b" : "app-a");
        harness.processElement(event, 1_000L);
        harness.processElement(event, 1_000L);
      }
      harness.setProcessingTime(EMIT_MS + 1L);

      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output).hasSize(3);
      assertThat(output).extracting(event -> event.getBkBizId() + ":" + event.getAppName())
          .containsExactlyInAnyOrder("1:app-a", "2:app-a", "1:app-b");
      assertThat(output).allSatisfy(event -> {
        assertThat(event.getSessionId()).isEqualTo("shared-session");
        assertThat(event.getActionCount()).isEqualTo(1L);
      });
    }
  }

  @Test
  void activeKeysTracksOpenAndClosedSessions() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      assertThat(activeKeys(harness)).isEqualTo(0L);

      harness.processElement(event("session-1", "action", "first.click", 1_000L), 1_000L);
      harness.processElement(event("session-1", "action", "second.click", 1_100L), 1_100L);
      harness.processElement(event("session-2", "action", "third.click", 1_200L), 1_200L);
      assertThat(activeKeys(harness)).isEqualTo(2L);

      harness.setProcessingTime(GAP_MS + 1L);
      assertThat(activeKeys(harness)).isEqualTo(0L);
    }
  }

  @Test
  void activeKeysIsRestoredFromCheckpoint() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> first =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(event("session-1", "action", "first.click", 1_000L), 1_000L);
      first.processElement(event("session-2", "action", "second.click", 1_100L), 1_100L);
      snapshot = first.snapshot(1L, 1L);
    }

    SessionizeProcessFunction function =
        new SessionizeProcessFunction(10_000L, GAP_MS, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, SessionEvent>
        restored =
            new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
                new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
                SessionizeProcessFunction::sessionKeyOf,
                TypeInformation.of(String.class),
                1,
                1,
                0)) {
      restored.initializeState(snapshot);
      restored.open();

      assertThat(function.activeKeys()).isEqualTo(2L);
      restored.setProcessingTime(GAP_MS + 1L);
      assertThat(function.activeKeys()).isEqualTo(0L);
    }
  }

  @Test
  void emitsIncrementalSnapshotThenFinalSnapshotAfterProcessingGap() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000L), 1_000L);
      harness.processElement(event("resource", "script.resource", 3_000L), 3_000L);

      harness.setProcessingTime(101L);
      List<SessionEvent> incremental = harness.extractOutputValues();
      assertThat(incremental.size()).isEqualTo(1);
      assertThat(incremental.get(0).isActive()).isTrue();
      assertThat(
          incremental.get(0).getActionCount() + incremental.get(0).getResourceCount())
          .isEqualTo(2);
      assertThat(incremental.get(0).getUpdatedAtTs()).isEqualTo(100_000L);

      harness.setProcessingTime(1_001L);
      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output.size()).isEqualTo(2);
      assertThat(output.get(1).isActive()).isFalse();
      assertThat(output.get(1).getUpdatedAtTs()).isEqualTo(1_000_000L);
      assertThat(output.get(1).getClosTim()).isEqualTo(1_000_000L);
      assertThat(output.get(1).getActionCount() + output.get(1).getResourceCount()).isEqualTo(2);
    }
  }

  @Test
  void unchangedEmitStopsAndNewEventRestartsIt() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS * 10, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000L), 1_000L);

      harness.setProcessingTime(101L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);
      harness.setProcessingTime(202L);
      assertThat(harness.extractOutputValues().size()).isEqualTo(1);

      harness.processElement(event("action", "second.click", 2_000L), 2_000L);
      harness.setProcessingTime(303L);
      List<SessionEvent> restarted = harness.extractOutputValues();
      assertThat(restarted.size()).isEqualTo(2);
      assertThat(restarted.get(1).isActive()).isTrue();
      assertThat(restarted.get(1).getActionCount()).isEqualTo(2);
    }
  }

  @Test
  void newEventExtendsSlidingGap() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000L), 1_000L);
      harness.setProcessingTime(500L);
      harness.processElement(event("action", "second.click", 2_000L), 2_000L);

      harness.setProcessingTime(1_001L);
      assertThat(harness.extractOutputValues().isEmpty()).isTrue();
      harness.setProcessingTime(1_501L);
      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output.size()).isEqualTo(1);
      assertThat(output.get(0).isActive()).isFalse();
      assertThat(output.get(0).getActionCount()).isEqualTo(2);
    }
  }

  @Test
  void sameProcessingTimeEventsReuseGapTimer() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000L), 1_000L);
      int timersAfterFirst = harness.numProcessingTimeTimers();
      harness.processElement(event("action", "second.click", 1_100L), 1_100L);
      assertThat(harness.numProcessingTimeTimers()).isEqualTo(timersAfterFirst);
    }
  }

  @Test
  void maxLifeRemainsFixedWhenGapIsExtended() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, 10_000L, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000L), 1_000L);
      harness.setProcessingTime(4_000L);
      harness.processElement(event("action", "second.click", 4_000L), 4_000L);

      harness.setProcessingTime(5_001L);
      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output.size()).isEqualTo(1);
      assertThat(output.get(0).isActive()).isFalse();
      assertThat(output.get(0).getActionCount()).isEqualTo(2);
    }
  }

  @Test
  void missingTenantDimensionsReachAuditWithoutOpeningWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      RumEvent missingBusiness = event("action", "missing-business", 1_000L);
      missingBusiness.getSpan().setBkBizId(null);
      RumEvent missingApp = event("action", "missing-app", 1_000L);
      missingApp.getSpan().setAppName(null);
      RumEvent missingSpan = event("action", "missing-span", 1_000L);
      missingSpan.setSpan(null);

      harness.processElement(missingBusiness, 1_000L);
      harness.processElement(missingApp, 1_000L);
      harness.processElement(missingSpan, 1_000L);

      assertThat(harness.extractOutputValues()).isEmpty();
      assertThat(activeKeys(harness)).isZero();
      assertThat(harness.getSideOutput(SessionizeProcessFunction.LATE_EVENTS)).hasSize(3);
    }
  }

  @Test
  void missingIdentityIsAuditedWithoutOpeningWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      RumEvent event = event("action", "missing-identity", 1_000L);
      event.getSpan().setSpanId(null);

      harness.processElement(event, 1_000L);

      assertThat(harness.extractOutputValues()).isEmpty();
      assertThat(activeKeys(harness)).isZero();
      assertThat(harness.getSideOutput(SessionizeProcessFunction.LATE_EVENTS)).hasSize(1);
    }
  }

  @Test
  void duplicateEventIdentityIsIgnored() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      RumEvent event = event("action", "click", 1_000L);

      harness.processElement(event, 1_000L);
      harness.processElement(event("action", "click", 1_000L), 1_000L);
      harness.setProcessingTime(101L);

      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output).hasSize(1);
      assertThat(output.get(0).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void restoredOverdueTimerAcceptsUnseenReplayWithoutDoubleCountingSeenEvent() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    String windowId;
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> first =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      first.processElement(event("action", "before-checkpoint", 1_000L), 1_000L);
      snapshot = first.snapshot(1L, 1L);
      first.setProcessingTime(EMIT_MS + 1L);
      windowId = first.extractOutputValues().get(0).getWindowId();
      assertThat(windowId).isNotBlank();
    }

    SessionizeProcessFunction function =
        new SessionizeProcessFunction(EMIT_MS, GAP_MS, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, SessionEvent>
        restored =
            new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
                new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
                SessionizeProcessFunction::sessionKeyOf,
                TypeInformation.of(String.class),
                1,
                1,
                0)) {
      restored.initializeState(snapshot);
      restored.open();
      restored.setProcessingTime(GAP_MS + 1L);
      assertThat(restored.extractOutputValues()).hasSize(2);

      restored.processElement(event("action", "before-checkpoint", 1_000L), 1_000L);
      assertThat(restored.extractOutputValues()).hasSize(2);

      RumEvent unseenReplay = event("action", "after-checkpoint", 2_000L);
      restored.processElement(unseenReplay, 2_000L);

      List<SessionEvent> output = restored.extractOutputValues();
      assertThat(output).hasSize(3);
      assertThat(output.get(2).isClosed()).isTrue();
      assertThat(output.get(2).getActionCount()).isEqualTo(2L);
      assertThat(restored.getSideOutput(SessionizeProcessFunction.LATE_EVENTS)).hasSize(1);
      assertThat(output).extracting(SessionEvent::getWindowId).containsOnly(windowId);
    }
  }

  @Test
  void duplicateAfterCloseDoesNotEmitAnotherFinalSnapshot() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      RumEvent event = event("action", "first.click", 1_000L);
      harness.processElement(event, 1_000L);
      harness.setProcessingTime(GAP_MS + 1L);
      assertThat(harness.extractOutputValues()).hasSize(2);

      harness.processElement(event("action", "first.click", 1_000L), 1_000L);

      assertThat(harness.extractOutputValues()).hasSize(2);
      assertThat(harness.getSideOutput(SessionizeProcessFunction.LATE_EVENTS)).isNull();
    }
  }

  @Test
  void correctionKeepsViewCountWhenBitmapWasRestored() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> first =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      RumEvent view = event("view", "first.view", 1_000L);
      view.getSpan().getAttributes().put("view.id", "v1");
      first.processElement(view, 1_000L);
      snapshot = first.snapshot(1L, 1L);
    }

    SessionizeProcessFunction function =
        new SessionizeProcessFunction(EMIT_MS, GAP_MS, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, SessionEvent>
        restored =
            new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
                new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
                SessionizeProcessFunction::sessionKeyOf,
                TypeInformation.of(String.class),
                1,
                1,
                0)) {
      restored.initializeState(snapshot);
      restored.open();
      restored.setProcessingTime(GAP_MS + 1L);

      RumEvent secondSpan = event("action", "second.click", 2_000L);
      secondSpan.getSpan().getAttributes().put("view.id", "v1");
      restored.processElement(secondSpan, 2_000L);
      RumEvent newView = event("view", "second.view", 3_000L);
      newView.getSpan().getAttributes().put("view.id", "v2");
      restored.processElement(newView, 3_000L);

      List<SessionEvent> output = restored.extractOutputValues();
      assertThat(output).hasSize(4);
      assertThat(output.get(2).getViewCount()).isEqualTo(1);
      assertThat(output.get(3).getViewCount()).isEqualTo(2);
    }
  }

  @Test
  void closedSessionCorrectsUntilCleanupThenAllowsFreshWindow() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(100L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000L), 1_000L);
      harness.setProcessingTime(1_001L);
      List<SessionEvent> closed = harness.extractOutputValues();
      assertThat(closed.size()).isEqualTo(2);
      assertThat(closed.get(1).isActive()).isFalse();

      String windowId = closed.get(0).getWindowId();
      assertThat(windowId).isNotBlank();
      assertThat(closed).extracting(SessionEvent::getWindowId).containsOnly(windowId);

      harness.processElement(event("action", "corrected.click", 2_000L), 2_000L);
      List<SessionEvent> corrected = harness.extractOutputValues();
      assertThat(corrected).hasSize(3);
      assertThat(corrected.get(2).isClosed()).isTrue();
      assertThat(corrected.get(2).getActionCount()).isEqualTo(2L);
      assertThat(corrected.get(2).getClosTim()).isEqualTo(closed.get(1).getClosTim());
      assertThat(corrected.get(2).getWindowId()).isEqualTo(windowId);

      harness.setProcessingTime(STATE_TTL_MS + 1_002L);
      harness.processElement(event("action", "reopened.click", 3_000L), 3_000L);
      harness.setProcessingTime(STATE_TTL_MS + 1_103L);
      List<SessionEvent> reopened = harness.extractOutputValues();
      assertThat(reopened.size()).isEqualTo(4);
      assertThat(reopened.get(3).isActive()).isTrue();
      assertThat(reopened.get(3).getActionCount()).isEqualTo(1);
      assertThat(reopened.get(3).getSessionId()).isEqualTo(closed.get(0).getSessionId());
      assertThat(reopened.get(3).getWindowId()).isNotBlank().isNotEqualTo(windowId);
    }
  }

  @Test
  void expiredWindowAuditsOldEventAndAllowsFreshGeneration() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "first.click", 1_000_000L), 1_000L);
      harness.setProcessingTime(GAP_MS + 1L);
      assertThat(harness.extractOutputValues()).hasSize(2);
      harness.processWatermark(5_000L);
      harness.setProcessingTime(STATE_TTL_MS + GAP_MS + 2L);

      harness.processElement(event("action", "expired.click", 2_000_000L), 2_000L);
      assertThat(harness.extractOutputValues()).hasSize(2);
      assertThat(harness.getSideOutput(SessionizeProcessFunction.LATE_EVENTS)).hasSize(1);

      harness.processElement(event("action", "fresh.click", 6_000_000L), 6_000L);
      harness.setProcessingTime(STATE_TTL_MS + GAP_MS + EMIT_MS + 3L);
      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output).hasSize(3);
      assertThat(output.get(2).isClosed()).isFalse();
      assertThat(output.get(2).getActionCount()).isEqualTo(1L);
    }
  }

  @Test
  void countsDistinctViewBucketsInDedicatedKeyedState() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      RumEvent first = event("view", "browser.view", 1_000L);
      first.getSpan().getAttributes().put("view.id", "view-1");
      RumEvent duplicate = event("view", "browser.view", 2_000L);
      duplicate.getSpan().getAttributes().put("view.id", "view-1");
      RumEvent second = event("view", "browser.view", 3_000L);
      second.getSpan().getAttributes().put("view.id", "view-2");

      harness.processElement(first, 1_000L);
      harness.processElement(duplicate, 2_000L);
      harness.processElement(second, 3_000L);
      harness.setProcessingTime(101L);

      assertThat(harness.extractOutputValues().get(0).getViewCount()).isEqualTo(2);
    }
  }

  @Test
  void viewBitmapStateIsRestoredFromCheckpoint() throws Exception {
    org.apache.flink.runtime.checkpoint.OperatorSubtaskState snapshot;
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> first =
        openHarness(EMIT_MS, GAP_MS, MAX_LIFE_MS)) {
      first.setProcessingTime(0L);
      RumEvent view = event("view", "browser.view", 1_000L);
      view.getSpan().getAttributes().put("view.id", "view-1");
      first.processElement(view, 1_000L);
      snapshot = first.snapshot(1L, 1L);
    }

    SessionizeProcessFunction function =
        new SessionizeProcessFunction(EMIT_MS, GAP_MS, MAX_LIFE_MS, STATE_TTL_MS);
    try (org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<
            String, RumEvent, SessionEvent>
        restored =
            new org.apache.flink.streaming.util.KeyedOneInputStreamOperatorTestHarness<>(
                new org.apache.flink.streaming.api.operators.KeyedProcessOperator<>(function),
                SessionizeProcessFunction::sessionKeyOf,
                TypeInformation.of(String.class),
                1,
                1,
                0)) {
      restored.initializeState(snapshot);
      restored.open();
      restored.setProcessingTime(0L);

      RumEvent duplicate = event("view", "browser.view", 2_000L);
      duplicate.getSpan().getAttributes().put("view.id", "view-1");
      RumEvent second = event("view", "browser.view", 3_000L);
      second.getSpan().getAttributes().put("view.id", "view-2");
      restored.processElement(duplicate, 2_000L);
      restored.processElement(second, 3_000L);
      restored.setProcessingTime(101L);

      assertThat(restored.extractOutputValues().get(0).getViewCount()).isEqualTo(2);
    }
  }

  @Test
  void sessionPhaseEndClosesImmediately() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      RumEvent end = event("session", "browser.session", 1_000L);
      end.getSpan().getAttributes().put("session.phase", "end");

      harness.processElement(end, 1_000L);

      List<SessionEvent> output = harness.extractOutputValues();
      assertThat(output).hasSize(1);
      assertThat(output.get(0).isClosed()).isTrue();
      assertThat(output.get(0).isActive()).isFalse();
      assertThat(output.get(0).getEndReason()).isEqualTo("normal");
      assertThat(activeKeys(harness)).isZero();
    }
  }

  @Test
  void eventTimeRangeStillUsesMinAndMaxForOutOfOrderEvents() throws Exception {
    try (OneInputStreamOperatorTestHarness<RumEvent, SessionEvent> harness =
        openHarness(10_000L, GAP_MS, MAX_LIFE_MS)) {
      harness.setProcessingTime(0L);
      harness.processElement(event("action", "late.click", 9_000L), 9_000L);
      harness.processElement(event("action", "early.click", 1_000L), 1_000L);
      harness.setProcessingTime(1_001L);

      SessionEvent output = harness.extractOutputValues().get(0);
      assertThat(output.getMinStartTime()).isEqualTo(1_000L);
      assertThat(output.getMaxEndTime()).isEqualTo(9_000L);
      assertThat(output.getActionCount()).isEqualTo(2);
    }
  }
}
