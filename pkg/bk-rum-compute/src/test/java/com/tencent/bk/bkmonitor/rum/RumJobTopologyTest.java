// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.Span;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;
import org.apache.flink.api.java.functions.KeySelector;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.flink.streaming.api.graph.StreamNode;
import org.apache.flink.util.ParameterTool;
import org.junit.jupiter.api.Test;

class RumJobTopologyTest {

  @Test
  void bothProductionBranchesPartitionByBusinessAppAndEntity() throws Exception {
    Map<String, String> values = requiredParameters();
    values.put("sink.type", "stdout");
    values.put("late.sink.type", "none");
    StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
    RumJobTopology.build(env, RumJobConfig.from(ParameterTool.fromMap(values)));
    List<StreamNode> aggregations = env.getStreamGraph().getStreamNodes().stream()
        .filter(node -> "session-precompute".equals(node.getOperatorName())
            || "view-precompute".equals(node.getOperatorName()))
        .collect(Collectors.toList());
    assertThat(aggregations).hasSize(2);

    for (StreamNode node : aggregations) {
      // 这两个聚合算子的输入均由拓扑限定为 RumEvent，StreamNode 的查询 API 擦除了该类型。
      @SuppressWarnings("unchecked")
      KeySelector<RumEvent, ?> selector = (KeySelector<RumEvent, ?>) node.getStatePartitioners()[0];
      Span span = new Span();
      span.setBkBizId(1);
      span.setAppName("app-a");
      span.setAttributes(Map.of("view.id", "shared-id"));
      RumEvent event = new RumEvent();
      event.setSpan(span);
      event.setSessionId("shared-id");
      Object originalKey = selector.getKey(event);

      span.setBkBizId(2);
      assertThat(selector.getKey(event)).isNotEqualTo(originalKey);
      span.setBkBizId(1);
      span.setAppName("app-b");
      assertThat(selector.getKey(event)).isNotEqualTo(originalKey);
      span.setAppName("app-a");
      assertThat(selector.getKey(event)).isEqualTo(originalKey);
    }
  }

  @Test
  void buildsBothAggregationBranchesWithStableOperatorNames() {
    Map<String, String> values = new HashMap<>();
    values.put("kafka.bootstrap.servers", "localhost:9092");
    values.put("kafka.topic", "rum-events");
    values.put("sink.type", "stdout");
    values.put("late.sink.type", "none");
    RumJobConfig config = RumJobConfig.from(ParameterTool.fromMap(values));
    StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();

    RumJobTopology.build(env, config);

    String plan = env.getExecutionPlan();
    assertThat(plan.contains("session-precompute")).isTrue();
    assertThat(plan.contains("view-precompute")).isTrue();
    assertThat(plan.contains("rum-events-source")).isTrue();
    assertThat(plan.contains("late-events-kafka-sink")).isFalse();
  }

  @Test
  void requiredParameterIsTrimmedAndValidated() {
    ParameterTool present =
        ParameterTool.fromMap(Map.of("kafka.bootstrap.servers", " localhost:9092 "));
    assertThat(
        RumJobConfig.requireNonBlank(present, "kafka.bootstrap.servers"))
        .isEqualTo("localhost:9092");

    ParameterTool blank = ParameterTool.fromMap(Map.of("kafka.bootstrap.servers", "  "));
    assertThatThrownBy(
        () -> RumJobConfig.requireNonBlank(blank, "kafka.bootstrap.servers"))
        .isInstanceOf(IllegalArgumentException.class);
  }

  @Test
  void defaultRetentionIsIndependentOfWindowDurations() {
    Map<String, String> values = new HashMap<>();
    values.put("kafka.bootstrap.servers", "localhost:9092");
    values.put("kafka.topic", "rum-events");
    values.put("session.gap.minutes", "45");
    values.put("session.max.life.minutes", "120");
    values.put("view.gap.minutes", "5");
    values.put("view.max.life.minutes", "10");
    RumJobConfig config =
        RumJobConfig.from(
            ParameterTool.fromMap(values));

    assertThat(config.sessionWindow().gapMinutes()).isEqualTo(45L);
    assertThat(config.sessionWindow().maxLifeMinutes()).isEqualTo(120L);
    assertThat(config.viewWindow().gapMinutes()).isEqualTo(5L);
    assertThat(config.viewWindow().maxLifeMinutes()).isEqualTo(10L);
    assertThat(config.sessionWindow().stateTtlMinutes()).isEqualTo(120L);
    assertThat(config.viewWindow().stateTtlMinutes()).isEqualTo(120L);
  }

  @Test
  void bothWindowsDefaultToFifteenMinuteGapAndFourHourMaxLife() {
    RumJobConfig config = RumJobConfig.from(ParameterTool.fromMap(requiredParameters()));

    for (RumJobConfig.WindowConfig window : List.of(config.sessionWindow(), config.viewWindow())) {
      assertThat(window.gapMinutes()).isEqualTo(15L);
      assertThat(window.maxLifeMinutes()).isEqualTo(240L);
      assertThat(window.gapMs()).isEqualTo(900_000L);
      assertThat(window.maxLifeMs()).isEqualTo(14_400_000L);
    }
  }

  @Test
  void correctionRetentionCanBeShorterThanGapAndMaxLife() {
    Map<String, String> values = requiredParameters();
    values.put("session.state.ttl.minutes", "1");
    values.put("view.state.ttl.minutes", "1");

    RumJobConfig config = RumJobConfig.from(ParameterTool.fromMap(values));

    assertThat(config.sessionWindow().stateTtlMs()).isEqualTo(60_000L);
    assertThat(config.viewWindow().stateTtlMs()).isEqualTo(60_000L);
  }

  @Test
  void rejectsInvalidEnumeratedModesBeforeTopologyBuild() {
    Map<String, String> invalidStartup = requiredParameters();
    invalidStartup.put("kafka.startup.mode", "from-middle");
    assertThatThrownBy(
        () -> RumJobConfig.from(ParameterTool.fromMap(invalidStartup)))
        .isInstanceOf(IllegalArgumentException.class);

    Map<String, String> invalidLateSink = requiredParameters();
    invalidLateSink.put("late.sink.type", "discard-ish");
    assertThatThrownBy(
        () -> RumJobConfig.from(ParameterTool.fromMap(invalidLateSink)))
        .isInstanceOf(IllegalArgumentException.class);
  }

  @Test
  void rejectsInvalidNumericAndWindowBoundaries() {
    Map<String, String> invalidParallelism = requiredParameters();
    invalidParallelism.put("parallelism", "0");
    assertThatThrownBy(
        () -> RumJobConfig.from(ParameterTool.fromMap(invalidParallelism)))
        .isInstanceOf(IllegalArgumentException.class);

    Map<String, String> invalidTtl = requiredParameters();
    invalidTtl.put("view.max.life.minutes", "5");
    invalidTtl.put("view.state.ttl.minutes", "0");
    assertThatThrownBy(
        () -> RumJobConfig.from(ParameterTool.fromMap(invalidTtl)))
        .isInstanceOf(IllegalArgumentException.class);
  }

  @Test
  void rejectsWindowMaxLifeNotGreaterThanGap() {
    // view.gap defaults to 15; setting view.max.life = 15 violates maxLife > gap.
    Map<String, String> equal = requiredParameters();
    equal.put("view.max.life.minutes", "15");
    assertThatThrownBy(() -> RumJobConfig.from(ParameterTool.fromMap(equal)))
        .isInstanceOf(IllegalArgumentException.class)
        .hasMessageContaining("view.max.life.minutes")
        .hasMessageContaining("view.gap.minutes");

    // session.max.life < session.gap also rejected, with the session prefix in the message.
    Map<String, String> inverted = requiredParameters();
    inverted.put("session.gap.minutes", "60");
    inverted.put("session.max.life.minutes", "30");
    assertThatThrownBy(() -> RumJobConfig.from(ParameterTool.fromMap(inverted)))
        .isInstanceOf(IllegalArgumentException.class)
        .hasMessageContaining("session.max.life.minutes")
        .hasMessageContaining("session.gap.minutes");
  }

  @Test
  void exposesNormalizedTypedConfiguration() {
    Map<String, String> values = requiredParameters();
    values.put("kafka.startup.mode", " EARLIEST ");
    values.put("sink.type", "elasticsearch");
    values.put("late.sink.type", "stdout");
    values.put("shutdown.savepoint.path", "  file:///savepoints  ");

    RumJobConfig config = RumJobConfig.from(ParameterTool.fromMap(values));

    assertThat(config.kafka().startupMode()).isEqualTo(RumJobConfig.StartupMode.EARLIEST);
    assertThat(config.sinkMode()).isEqualTo(RumJobConfig.SinkMode.ELASTICSEARCH);
    assertThat(config.lateSinkMode()).isEqualTo(RumJobConfig.LateSinkMode.STDOUT);
    assertThat(config.shutdown().savepointPath()).isEqualTo("file:///savepoints");
    assertThat(config.viewWindow().emitMs()).isEqualTo(30_000L);
    assertThat(config.kafka().partitionDiscoveryIntervalMs()).isEqualTo(300_000L);
  }

  @Test
  void allowsExplicitPartitionDiscoveryInterval() {
    Map<String, String> values = requiredParameters();
    values.put("kafka.partition.discovery.interval.ms", "60000");

    RumJobConfig config = RumJobConfig.from(ParameterTool.fromMap(values));

    assertThat(config.kafka().partitionDiscoveryIntervalMs()).isEqualTo(60_000L);
  }

  @Test
  void rejectsNonPositivePartitionDiscoveryInterval() {
    Map<String, String> values = requiredParameters();
    values.put("kafka.partition.discovery.interval.ms", "0");

    assertThatThrownBy(() -> RumJobConfig.from(ParameterTool.fromMap(values)))
        .isInstanceOf(IllegalArgumentException.class);
  }

  private static Map<String, String> requiredParameters() {
    Map<String, String> values = new HashMap<>();
    values.put("kafka.bootstrap.servers", "localhost:9092");
    values.put("kafka.topic", "rum-events");
    return values;
  }
}
