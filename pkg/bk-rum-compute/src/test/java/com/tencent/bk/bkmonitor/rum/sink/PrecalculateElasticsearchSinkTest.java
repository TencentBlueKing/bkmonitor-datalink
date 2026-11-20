// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateFamily;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateRecord;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorage;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorageCache;
import com.tencent.bk.bkmonitor.rum.utils.RumEntityKey;
import java.time.Duration;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import javax.annotation.Nullable;

import com.tencent.bk.bkmonitor.rum.sink.session.SessionPrecalculateEmitter;
import com.tencent.bk.bkmonitor.rum.sink.view.ViewPrecalculateEmitter;
import org.apache.flink.connector.elasticsearch.sink.RequestIndexer;
import org.apache.flink.util.InstantiationUtil;
import org.apache.flink.util.ParameterTool;
import org.elasticsearch.action.index.IndexRequest;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.NullAndEmptySource;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * {@link PrecalculateElasticsearchSink} emitter 单元测试。
 *
 * <p>通过自定义 {@link RequestIndexer} 捕获实际写入的 {@link IndexRequest}，验证：
 * ① 路由选择正确的分表写入别名；② document id 与 routing 一致；③ 缺失字段时跳过；④
 * toDocument 与现有实时 sink 字段集合一致。
 */
class PrecalculateElasticsearchSinkTest {

  private static final String WINDOW_ID = "00000000-0000-0000-0000-000000000001";
  private static final String REOPENED_WINDOW_ID = "00000000-0000-0000-0000-000000000002";

  @Test
  void reopenedWindowsUseDistinctDocumentsAndCorrectionsKeepOriginalIds() {
    SessionPrecalculateEmitter sessions = new SessionPrecalculateEmitter(1L, 100, 60, 1);
    ViewPrecalculateEmitter views = new ViewPrecalculateEmitter(1L, 100, 60, 1);
    sessions.open();
    views.open();
    CapturingIndexer sessionRequests = new CapturingIndexer();
    CapturingIndexer viewRequests = new CapturingIndexer();

    for (String windowId : List.of(WINDOW_ID, REOPENED_WINDOW_ID, WINDOW_ID)) {
      SessionEvent session = sampleSession("session", 1, "app");
      session.setWindowId(windowId);
      sessions.emit(session, null, sessionRequests);
      ViewEventDocument view = sampleView("view", "session", 1, "app");
      view.setWindowId(windowId);
      views.emit(view, null, viewRequests);
    }

    for (CapturingIndexer indexer : List.of(sessionRequests, viewRequests)) {
      assertThat(indexer.requests).hasSize(3);
      IndexRequest original = indexer.requests.get(0);
      IndexRequest reopened = indexer.requests.get(1);
      IndexRequest correction = indexer.requests.get(2);
      assertThat(original.id()).endsWith(":" + WINDOW_ID);
      assertThat(reopened.id()).endsWith(":" + REOPENED_WINDOW_ID).isNotEqualTo(original.id());
      assertThat(correction.id()).isEqualTo(original.id());
      assertThat(indexer.requests).extracting(IndexRequest::index).containsOnly(original.index());
      assertThat(indexer.requests).extracting(request -> request.sourceAsMap().get("window_id"))
          .containsExactly(WINDOW_ID, REOPENED_WINDOW_ID, WINDOW_ID);
    }
  }

  @ParameterizedTest
  @NullAndEmptySource
  @ValueSource(strings = {" "})
  void missingWindowIdSkipsEmit(@Nullable String windowId) {
    SessionPrecalculateEmitter sessions = new SessionPrecalculateEmitter(1L, 100, 60, 1);
    ViewPrecalculateEmitter views = new ViewPrecalculateEmitter(1L, 100, 60, 1);
    sessions.open();
    views.open();
    CapturingIndexer indexer = new CapturingIndexer();
    SessionEvent session = sampleSession("session", 1, "app");
    session.setWindowId(windowId);
    ViewEventDocument view = sampleView("view", "session", 1, "app");
    view.setWindowId(windowId);

    sessions.emit(session, null, indexer);
    views.emit(view, null, indexer);

    assertThat(indexer.requests).isEmpty();
  }

  @ParameterizedTest
  @ValueSource(ints = {1, 7})
  void nonDefaultShardCountsSurviveSerialization(int dispersedCount) throws Exception {
    SessionPrecalculateEmitter sessions = new SessionPrecalculateEmitter(1L, 100, 60, dispersedCount);
    ViewPrecalculateEmitter views = new ViewPrecalculateEmitter(1L, 100, 60, dispersedCount);
    GenericPrecalculateEmitter<PrecalculateRecord> generic =
        new GenericPrecalculateEmitter<>(PrecalculateFamily.SESSION, 1L, 100, 60, dispersedCount);
    sessions.open();
    views.open();
    generic.open();

    sessions = InstantiationUtil.deserializeObject(
        InstantiationUtil.serializeObject(sessions), getClass().getClassLoader());
    views = InstantiationUtil.deserializeObject(
        InstantiationUtil.serializeObject(views), getClass().getClassLoader());
    generic = InstantiationUtil.deserializeObject(
        InstantiationUtil.serializeObject(generic), getClass().getClassLoader());
    sessions.open();
    views.open();
    generic.open();
    CapturingIndexer sessionRequests = new CapturingIndexer();
    CapturingIndexer viewRequests = new CapturingIndexer();
    CapturingIndexer genericRequests = new CapturingIndexer();
    for (int bizId = 1; bizId <= 30; bizId++) {
      sessions.emit(sampleSession("session", bizId, "app"), null, sessionRequests);
      views.emit(sampleView("view", "session", bizId, "app"), null, viewRequests);
      generic.emit(sampleRecord(bizId), null, genericRequests);
      String expectedSessionIndex = PrecalculateStorage.forApp(
          PrecalculateFamily.SESSION, bizId, "app", 1L, LocalDate.of(2026, 9, 10), dispersedCount).writeAlias;
      assertThat(sessionRequests.requests.get(bizId - 1).index()).isEqualTo(expectedSessionIndex);
      assertThat(genericRequests.requests.get(bizId - 1).index()).isEqualTo(expectedSessionIndex);
      assertThat(viewRequests.requests.get(bizId - 1).index()).isEqualTo(
          PrecalculateStorage.forApp(PrecalculateFamily.VIEW, bizId, "app", 1L,
              LocalDate.of(2026, 9, 10), dispersedCount).writeAlias);
    }
    if (dispersedCount == 7) {
      assertThat(sessionRequests.requests).anySatisfy(request ->
          assertThat(request.index()).endsWith("_7"));
      assertThat(viewRequests.requests).anySatisfy(request ->
          assertThat(request.index()).endsWith("_7"));
    }
  }

  @ParameterizedTest
  @ValueSource(ints = {0, -1})
  void sinkFactoriesRejectNonPositiveShardCount(int dispersedCount) {
    ParameterTool parameters = ParameterTool.fromMap(
        Map.of("precalculate.dispersed-count", Integer.toString(dispersedCount)));
    assertThatThrownBy(() -> PrecalculateElasticsearchSink.forSession(parameters))
        .isInstanceOf(IllegalArgumentException.class).hasMessageContaining("dispersedCount");
    assertThatThrownBy(() -> PrecalculateElasticsearchSink.forView(parameters))
        .isInstanceOf(IllegalArgumentException.class).hasMessageContaining("dispersedCount");
    assertThatThrownBy(() -> PrecalculateElasticsearchSink.create(parameters, PrecalculateFamily.VIEW))
        .isInstanceOf(IllegalArgumentException.class).hasMessageContaining("dispersedCount");
  }

  @Test
  void configuringAnotherSinkDoesNotChangeSerializedEmitterRouting() throws Exception {
    SessionPrecalculateEmitter original = new SessionPrecalculateEmitter(1L, 100, 60);
    original.open();
    CapturingIndexer before = new CapturingIndexer();
    for (int bizId = 1; bizId <= 20; bizId++) {
      original.emit(sampleSession("session", bizId, "app"), null, before);
    }
    SessionPrecalculateEmitter restored = InstantiationUtil.deserializeObject(
        InstantiationUtil.serializeObject(original), getClass().getClassLoader());

    try {
      PrecalculateElasticsearchSink.forSession(
          ParameterTool.fromMap(Map.of("precalculate.dispersed-count", "1")));
      restored.open();
      CapturingIndexer after = new CapturingIndexer();
      for (int bizId = 1; bizId <= 20; bizId++) {
        restored.emit(sampleSession("session", bizId, "app"), null, after);
      }

      assertThat(after.requests).extracting(IndexRequest::index)
          .containsExactlyElementsOf(
              before.requests.stream().map(IndexRequest::index).collect(java.util.stream.Collectors.toList()));
    } finally {
      PrecalculateElasticsearchSink.forSession(
          ParameterTool.fromMap(Map.of("precalculate.dispersed-count", "5")));
    }
  }

  @Test
  void documentIdsIncludeBusinessAndAppWhileKeepingOriginalEntityFields() {
    SessionPrecalculateEmitter sessions = new SessionPrecalculateEmitter(1L, 100, 60, 1);
    ViewPrecalculateEmitter views = new ViewPrecalculateEmitter(1L, 100, 60, 1);
    sessions.open();
    views.open();
    CapturingIndexer sessionRequests = new CapturingIndexer();
    CapturingIndexer viewRequests = new CapturingIndexer();
    for (int i = 0; i < 3; i++) {
      int bizId = i == 1 ? 2 : 1;
      String appName = i == 2 ? "app-b" : "app-a";
      sessions.emit(sampleSession("same-id", bizId, appName), null, sessionRequests);
      views.emit(sampleView("same-id", "session", bizId, appName), null, viewRequests);
    }

    assertThat(sessionRequests.requests).extracting(IndexRequest::id).doesNotHaveDuplicates();
    assertThat(viewRequests.requests).extracting(IndexRequest::id).doesNotHaveDuplicates();
    assertThat(sessionRequests.requests).extracting(IndexRequest::index)
        .containsOnly("write_20260910_rum_global_session_precalculate_auto_1");
    assertThat(viewRequests.requests).extracting(IndexRequest::index)
        .containsOnly("write_20260910_rum_global_view_precalculate_auto_1");
    assertThat(sessionRequests.requests).allSatisfy(request ->
        assertThat(request.sourceAsMap()).containsEntry("attributes.session.id", "same-id"));
    assertThat(viewRequests.requests).allSatisfy(request ->
        assertThat(request.sourceAsMap()).containsEntry("attributes.view.id", "same-id"));

    sessions.emit(sampleSession("same-id", 1, "app-a"), null, sessionRequests);
    views.emit(sampleView("same-id", "session", 1, "app-a"), null, viewRequests);
    assertThat(sessionRequests.requests.get(3).id()).isEqualTo(sessionRequests.requests.get(0).id());
    assertThat(viewRequests.requests.get(3).id()).isEqualTo(viewRequests.requests.get(0).id());
  }

  @Test
  void longUnicodeIdentityProducesValidDistinctDocumentIds() {
    SessionPrecalculateEmitter sessions = new SessionPrecalculateEmitter(1L, 100, 60, 1);
    ViewPrecalculateEmitter views = new ViewPrecalculateEmitter(1L, 100, 60, 1);
    sessions.open();
    views.open();
    CapturingIndexer requests = new CapturingIndexer();
    String prefix = "应用".repeat(300);

    for (String app : List.of(prefix + "甲", prefix + "乙")) {
      sessions.emit(sampleSession("session", 1, app), null, requests);
      views.emit(sampleView("view", "session", 1, app), null, requests);
    }

    assertThat(requests.requests).extracting(IndexRequest::id).doesNotHaveDuplicates();
    assertThat(requests.requests).allSatisfy(request -> {
      assertThat(request.id()).hasSize(101);
      assertThat(request.validate()).isNull();
    });
  }

  // ────────────────────────── View emitter ───────────────────────

  @Nested
  class ViewEmitterTest {

    @Test
    void emitsIndexRequestWithCorrectWriteAlias() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      ViewPrecalculateEmitter emitter = ViewPrecalculateEmitter.withCache(cache);

      ViewEventDocument doc = sampleView("v1", "s1", (int) 123, "myApp");
      CapturingIndexer indexer = new CapturingIndexer();
      emitter.emit(doc, null, indexer);

      assertThat(indexer.requests.size()).isEqualTo(1);
      IndexRequest req = indexer.requests.get(0);
      assertThat(
          req.index().matches("write_\\d{8}_rum_global_view_precalculate_auto_\\d"))
          .withFailMessage("writeAlias 应形如 write_yyyyMMdd_rum_global_view_precalculate_auto_N，实际：" + req.index())
          .isTrue();
      assertThat(req.id()).isEqualTo(RumEntityKey.documentId(123, "myApp", "v1", WINDOW_ID));
      assertThat(req.index()).startsWith("write_20260910_");
    }

    @Test
    void usesConfiguredDocumentDateInsteadOfCurrentDateForRouting() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      ViewPrecalculateEmitter emitter = ViewPrecalculateEmitter.withCache(cache);
      ViewEventDocument doc = sampleView("v-old", "s-old", 1, "app");
      doc.setDate("2020-01-02");
      CapturingIndexer indexer = new CapturingIndexer();

      emitter.emit(doc, null, indexer);

      assertThat(indexer.requests.get(0).index()).startsWith("write_20200102_");
    }

    @Test
    void routingDependsOnAppName() {
      // 10 个不同 appName 应分散到 ≥2 个分表（统计上几乎不会全撞）
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      ViewPrecalculateEmitter emitter = ViewPrecalculateEmitter.withCache(cache);

      java.util.Set<String> uniqueIndices = new java.util.HashSet<>();
      for (int i = 0; i < 10; i++) {
        CapturingIndexer indexer = new CapturingIndexer();
        emitter.emit(sampleView("v" + i, "s" + i, (int) 1, "app" + i), null, indexer);
        uniqueIndices.add(indexer.requests.get(0).index());
      }
      assertThat(
          uniqueIndices.size() >= 2)
          .withFailMessage("10 个不同 appName 应分散到 ≥2 个分表，实际：" + uniqueIndices)
          .isTrue();
    }

    @Test
    void missingBkBizIdSkipsEmit() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      ViewPrecalculateEmitter emitter = ViewPrecalculateEmitter.withCache(cache);

      ViewEventDocument doc = sampleView("v1", "s1", null, "app");
      CapturingIndexer indexer = new CapturingIndexer();
      emitter.emit(doc, null, indexer);
      assertThat(indexer.requests.size()).isEqualTo(0);
    }

    @Test
    void missingAppNameSkipsEmit() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      ViewPrecalculateEmitter emitter = ViewPrecalculateEmitter.withCache(cache);

      ViewEventDocument doc = sampleView("v1", "s1", (int) 1, null);
      CapturingIndexer indexer = new CapturingIndexer();
      emitter.emit(doc, null, indexer);
      assertThat(indexer.requests.size()).isEqualTo(0);
    }

    @Test
    void blankAppNameSkipsEmit() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      ViewPrecalculateEmitter emitter = ViewPrecalculateEmitter.withCache(cache);

      ViewEventDocument doc = sampleView("v1", "s1", (int) 1, "   ");
      CapturingIndexer indexer = new CapturingIndexer();
      emitter.emit(doc, null, indexer);
      assertThat(indexer.requests.size()).isEqualTo(0);
    }

    @Test
    void toDocumentHasViewFields() {
      ViewEventDocument doc = sampleView("v1", "s1", (int) 123, "myApp");
      Map<String, Object> m = ViewPrecalculateEmitter.toDocument(doc);
      assertThat(m.get("attributes.view.id")).isEqualTo("v1");
      assertThat(m.get("attributes.session.id")).isEqualTo("s1");
      assertThat(m.get("app_name")).isEqualTo("myApp");
      assertThat(m.get("bk_biz_id")).isEqualTo(123);
      assertThat(m.get("date")).isNotNull();
      assertThat(m).containsKey("close_reason");
    }
  }

  // ────────────────────────── Session emitter ────────────────────

  @Nested
  class SessionEmitterTest {

    @Test
    void emitsIndexRequestWithCorrectWriteAlias() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(7L, 100, Duration.ofMinutes(60));
      SessionPrecalculateEmitter emitter = SessionPrecalculateEmitter.withCache(cache);

      SessionEvent doc = sampleSession("sess1", (int) 123, "myApp");
      CapturingIndexer indexer = new CapturingIndexer();
      emitter.emit(doc, null, indexer);

      assertThat(indexer.requests.size()).isEqualTo(1);
      IndexRequest req = indexer.requests.get(0);
      assertThat(
          req.index().matches("write_\\d{8}_rum_global_session_precalculate_auto_\\d"))
          .withFailMessage("writeAlias 应形如 write_yyyyMMdd_rum_global_session_precalculate_auto_N，实际：" + req.index())
          .isTrue();
      assertThat(req.id()).isEqualTo(RumEntityKey.documentId(123, "myApp", "sess1", WINDOW_ID));
      assertThat(req.index()).startsWith("write_20260910_");
    }

    @Test
    void toDocumentHasSessionFields() {
      SessionEvent doc = sampleSession("sess1", (int) 123, "myApp");
      Map<String, Object> m = SessionPrecalculateEmitter.toDocument(doc);
      assertThat(m.get("attributes.session.id")).isEqualTo("sess1");
      assertThat(m.get("app_name")).isEqualTo("myApp");
      assertThat(m.get("bk_biz_id")).isEqualTo(123);
      assertThat(m.get("date")).isNotNull();
      assertThat(m.get("closed")).isEqualTo(true);
    }

    @Test
    void usesViewFieldNamesForSessionAggregates() {
      SessionEvent doc = sampleSession("sess1", (int) 123, "myApp");
      doc.setEndReason("timeout");
      doc.setResourceCount(7);
      doc.setRequestCount(3);
      doc.setAvgLoadTimeUs(11);
      doc.setMaxLcpUs(12);
      doc.setMaxInpUs(13);
      doc.setMaxCls(14D);

      Map<String, Object> fields = SessionPrecalculateEmitter.toDocument(doc);

      assertThat(fields.get("close_reason")).isEqualTo("timeout");
      assertThat(fields.get("resource_count")).isEqualTo(7L);
      assertThat(fields.get("request_count")).isEqualTo(3);
      assertThat(fields.get("attributes.view.loading_time")).isEqualTo(11L);
      assertThat(fields.get("web_vitals.lcp")).isEqualTo(12L);
      assertThat(fields.get("web_vitals.inp")).isEqualTo(13L);
      assertThat(fields.get("web_vitals.cls")).isEqualTo(14D);
      assertThat(fields).doesNotContainKeys(
          "end_reason",
          "avg_load_time_us",
          "max_lcp_us",
          "max_inp_us",
          "max_cls");
    }

    @Test
    void missingBkBizIdSkipsEmit() {
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));
      SessionPrecalculateEmitter emitter = SessionPrecalculateEmitter.withCache(cache);

      SessionEvent doc = sampleSession("sess1", null, "myApp");
      CapturingIndexer indexer = new CapturingIndexer();
      emitter.emit(doc, null, indexer);
      assertThat(indexer.requests.size()).isEqualTo(0);
    }

    @Test
    void viewAndSessionRouteIndependently() {
      // 同一 (bkBizId, appName) 在 VIEW 和 SESSION 应独立路由
      PrecalculateStorageCache cache =
          new PrecalculateStorageCache(3L, 100, Duration.ofMinutes(60));

      CapturingIndexer viewIdx = new CapturingIndexer();
      CapturingIndexer sessIdx = new CapturingIndexer();

      ViewPrecalculateEmitter.withCache(cache)
          .emit(sampleView("v1", "s1", (int) 1, "app"), null, viewIdx);
      SessionPrecalculateEmitter.withCache(cache)
          .emit(sampleSession("sess1", (int) 1, "app"), null, sessIdx);

      assertThat(viewIdx.requests.get(0).index().contains("_view_")).isTrue();
      assertThat(sessIdx.requests.get(0).index().contains("_session_")).isTrue();
    }
  }

  // ────────────────────────── Deserialization ────────────────────
  //
  // 模拟 Flink 在 restart 时对 emitter 序列化的场景，验证 transient cache 字段在反序列化后
  // 为 null，并通过 open() 按配置重建。

  @Nested
  class DeserializationTest {

    @Test
    void viewEmitterRebuildsCacheAfterDeserialization() throws Exception {
      ViewPrecalculateEmitter original = new ViewPrecalculateEmitter(3L, 100, 60);
      original.open(); // 模拟 Flink 启动后第一次 open()
      ViewEventDocument doc = sampleView("v1", "s1", (int) 123, "myApp");
      CapturingIndexer idxBefore = new CapturingIndexer();
      original.emit(doc, null, idxBefore);
      String writeAliasBefore = idxBefore.requests.get(0).index();

      // 序列化 → 反序列化（模拟 Flink checkpoint → restart）
      java.io.ByteArrayOutputStream baos = new java.io.ByteArrayOutputStream();
      try (java.io.ObjectOutputStream oos = new java.io.ObjectOutputStream(baos)) {
        oos.writeObject(original);
      }
      ViewPrecalculateEmitter restored;
      try (java.io.ObjectInputStream ois =
          new java.io.ObjectInputStream(new java.io.ByteArrayInputStream(baos.toByteArray()))) {
        restored = (ViewPrecalculateEmitter) ois.readObject();
      }

      // transient cache 在反序列化后是 null，但 config 还在
      java.lang.reflect.Field cacheField =
          AbstractPrecalculateEmitter.class.getDeclaredField("cache");
      cacheField.setAccessible(true);
      assertThat(
          cacheField.get(restored))
          .withFailMessage("transient cache 字段反序列化后应为 null")
          .isNull();

      // open() 应按配置重建缓存
      restored.open();
      CapturingIndexer idxAfter = new CapturingIndexer();
      restored.emit(sampleView("v2", "s1", (int) 123, "myApp"), null, idxAfter);
      assertThat(idxAfter.requests.size()).isEqualTo(1);
      assertThat(
          idxAfter.requests.get(0).index())
          .withFailMessage("反序列化后 emit 应路由到同一分表（基于相同的 clusterId + bkBizId + appName）")
          .isEqualTo(writeAliasBefore);
    }

    @Test
    void sessionEmitterRebuildsCacheAfterDeserialization() throws Exception {
      SessionPrecalculateEmitter original = new SessionPrecalculateEmitter(7L, 100, 60);
      original.open();

      java.io.ByteArrayOutputStream baos = new java.io.ByteArrayOutputStream();
      try (java.io.ObjectOutputStream oos = new java.io.ObjectOutputStream(baos)) {
        oos.writeObject(original);
      }
      SessionPrecalculateEmitter restored;
      try (java.io.ObjectInputStream ois =
          new java.io.ObjectInputStream(new java.io.ByteArrayInputStream(baos.toByteArray()))) {
        restored = (SessionPrecalculateEmitter) ois.readObject();
      }

      java.lang.reflect.Field cacheField =
          AbstractPrecalculateEmitter.class.getDeclaredField("cache");
      cacheField.setAccessible(true);
      assertThat(cacheField.get(restored)).isNull();

      restored.open();
      CapturingIndexer idx = new CapturingIndexer();
      restored.emit(sampleSession("sess1", (int) 1, "app"), null, idx);
      assertThat(idx.requests.size()).isEqualTo(1);
      assertThat(idx.requests.get(0).index().contains("_session_")).isTrue();
    }

    @Test
    void openIsIdempotent() {
      // open() 调用多次不会重建缓存（避免无谓的 Caffeine 重建）
      ViewPrecalculateEmitter emitter = new ViewPrecalculateEmitter(3L, 100, 60);
      emitter.open();
      java.lang.reflect.Field cacheField;
      try {
        cacheField =
            AbstractPrecalculateEmitter.class.getDeclaredField("cache");
        cacheField.setAccessible(true);
        Object cacheBefore = cacheField.get(emitter);
        emitter.open();
        emitter.open();
        Object cacheAfter = cacheField.get(emitter);
        assertThat(cacheAfter).withFailMessage("重复 open() 不应重建缓存").isSameAs(cacheBefore);
      } catch (Exception e) {
        throw new RuntimeException(e);
      }
    }
  }

  // ────────────────────────── 辅助：构造样本 doc ──────────────────

  private static PrecalculateRecord sampleRecord(long bizId) {
    return new PrecalculateRecord() {
      @Override
      public long bkBizId() {
        return bizId;
      }

      @Override
      public String appName() {
        return "app";
      }

      @Override
      public String documentId() {
        return "record";
      }

      @Override
      public String indexDate() {
        return "2026-09-10";
      }

      @Override
      public Map<String, Object> toDocument() {
        return Map.of("bk_biz_id", bizId);
      }
    };
  }

  private static ViewEventDocument sampleView(
      String viewId, String sessionId, Integer bkBizId, String appName) {
    ViewEventDocument d = new ViewEventDocument();
    d.setViewId(viewId);
    d.setWindowId(WINDOW_ID);
    d.setSessionId(sessionId);
    d.setBkBizId(bkBizId);
    d.setAppName(appName);
    d.setDate("2026-09-10");
    d.setClosed(true);
    d.setActive(false);
    return d;
  }

  private static SessionEvent sampleSession(String sessionId, Integer bkBizId, String appName) {
    SessionEvent s = new SessionEvent();
    s.setSessionId(sessionId);
    s.setWindowId(WINDOW_ID);
    s.setBkBizId(bkBizId);
    s.setAppName(appName);
    s.setDate("2026-09-10");
    s.setMinStartTime(1_700_000_000_000L);
    s.setMaxEndTime(1_700_000_100_000L);
    s.setActive(false);
    s.setClosed(true);
    return s;
  }

  /**
   * 捕获所有 add 调用的测试 indexer。
   *
   * <p>{@link RequestIndexer} 接口只有 4 个方法（IndexRequest / UpdateRequest / DeleteRequest 各一个 varargs
   * 版本，加 flush），实现只关心 IndexRequest，其它忽略。
   */
  private static final class CapturingIndexer implements RequestIndexer {
    final List<IndexRequest> requests = new ArrayList<>();

    @Override
    public void add(IndexRequest... requests) {
      for (IndexRequest r : requests) {
        this.requests.add(r);
      }
    }

    @Override
    public void add(org.elasticsearch.action.update.UpdateRequest... requests) {
      // 测试只关心 IndexRequest，UpdateRequest 直接忽略
    }

    @Override
    public void add(org.elasticsearch.action.delete.DeleteRequest... requests) {
      // 同上
    }

    @Override
    public void flush() {}
  }

  // 显式抑制「未使用 import」警告：LocalDate 仅在文档注释里出现
  @SuppressWarnings("unused")
  private static final LocalDate UNUSED_TODAY = LocalDate.of(2026, 9, 10);
}
