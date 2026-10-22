// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.process.SessionizeProcessFunction;
import com.tencent.bk.bkmonitor.rum.process.ViewProcessFunction;
import com.tencent.bk.bkmonitor.rum.serde.RumEventDeserializationSchema;
import com.tencent.bk.bkmonitor.rum.serde.RumEventSerializationSchema;
import com.tencent.bk.bkmonitor.rum.sink.PrecalculateElasticsearchSink;
import java.time.Duration;
import org.apache.flink.api.common.eventtime.WatermarkStrategy;
import org.apache.flink.connector.base.DeliveryGuarantee;
import org.apache.flink.connector.kafka.sink.KafkaRecordSerializationSchema;
import org.apache.flink.connector.kafka.sink.KafkaSink;
import org.apache.flink.connector.kafka.source.KafkaSource;
import org.apache.flink.connector.kafka.source.enumerator.initializer.OffsetsInitializer;
import org.apache.flink.streaming.api.datastream.DataStream;
import org.apache.flink.streaming.api.datastream.SingleOutputStreamOperator;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.kafka.clients.consumer.OffsetResetStrategy;

/**
 * RUM 作业拓扑装配层。
 *
 * <p>这里只负责 Source -> Process -> Sink 的数据流组装；作业提交、Web UI 兼容和优雅停机留在
 * {@link RumPrecomputeJob}。这个边界让拓扑修改不再牵动进程生命周期代码。
 */
public final class RumJobTopology {
  private RumJobTopology() {}

  /**
   * 装配完整作业 DAG。
   *
   * <p>这个方法只向 {@link StreamExecutionEnvironment} 注册算子，不执行或提交作业，因此可在单测中直接检查执行计划。
   */
  public static void build(StreamExecutionEnvironment env, RumJobConfig config) {
    KafkaSource<RumEvent> source = createSource(config.kafka(), config.jobName());
    WatermarkStrategy<RumEvent> watermarkStrategy = createWatermarkStrategy(config.watermark());
    DataStream<RumEvent> events =
        env.fromSource(source, watermarkStrategy, "rum-events-source")
            .name("rum-events-source")
            .uid("rum-events-source");

    // 同一条解析后事件流独立分叉：两条链路各自 keyBy，不共享聚合状态。
    SingleOutputStreamOperator<SessionEvent> sessions =
        buildSessionStream(events, config.sessionWindow());
    writeSessionSink(config, sessions);
    writeLateEventSink(config, sessions.getSideOutput(SessionizeProcessFunction.LATE_EVENTS));

    SingleOutputStreamOperator<ViewEventDocument> views =
        buildViewStream(events, config.viewWindow());
    writeViewSink(config, views);
  }

  private static KafkaSource<RumEvent> createSource(
      RumJobConfig.KafkaConfig config, String jobName) {
    // 默认采用攒批 fetch，降低高 QPS 下的小请求和 broker CPU 开销；所有值已由 RumJobConfig 校验。
    // receive.buffer.bytes 调大以减少单次拉取的 syscall 次数；connections.max.idle.ms 避免空闲连接频繁重建。
    // client.id.prefix 用 jobName 命名，让 Kafka 端能区分多套部署；该 Source 同时供 Session 与 View 两条链路复用。
    return KafkaSource.<RumEvent>builder()
        .setBootstrapServers(config.bootstrapServers())
        .setTopics(config.topic())
        .setGroupId(config.groupId())
        .setProperty("client.id.prefix", jobName + "-")
        .setProperty("fetch.min.bytes", Long.toString(config.fetchMinBytes()))
        .setProperty("fetch.max.wait.ms", Long.toString(config.fetchMaxWaitMs()))
        .setProperty("max.partition.fetch.bytes", Long.toString(config.maxPartitionFetchBytes()))
        .setProperty("max.poll.records", Integer.toString(config.maxPollRecords()))
        .setProperty("receive.buffer.bytes", Integer.toString(config.receiveBufferBytes()))
        .setProperty(
            "connections.max.idle.ms", Long.toString(config.connectionsMaxIdleMs()))
        .setProperty(
            "partition.discovery.interval.ms",
            Long.toString(config.partitionDiscoveryIntervalMs()))
        .setStartingOffsets(startingOffsets(config.startupMode()))
        .setDeserializer(new RumEventDeserializationSchema())
        .build();
  }

  private static WatermarkStrategy<RumEvent> createWatermarkStrategy(
      RumJobConfig.WatermarkConfig config) {
    // idleness 防止无数据的 Kafka partition 长期卡住全局 watermark。
    return WatermarkStrategy.<RumEvent>forBoundedOutOfOrderness(
            Duration.ofSeconds(config.outOfOrdernessSeconds()))
        // 事件时间在链路内为微秒；Flink watermark 域固定为毫秒，此处降精度对齐。
        .withTimestampAssigner(
            (event, recordTimestamp) -> event.getEventTime() / TimeUtils.US_TO_MS)
        .withIdleness(Duration.ofMinutes(config.idleMinutes()));
  }

  private static SingleOutputStreamOperator<SessionEvent> buildSessionStream(
      DataStream<RumEvent> events, RumJobConfig.WindowConfig window) {
    // UID 是 savepoint 中状态映射的稳定标识，重构时不应随意更改。
    return events
        .filter(RumEvent::hasSessionAndTime)
        .name("drop-malformed-events")
        .uid("drop-malformed-events")
        .keyBy(SessionizeProcessFunction::sessionKeyOf)
        .process(
            new SessionizeProcessFunction(
                window.emitMs(), window.gapMs(), window.maxLifeMs(), window.stateTtlMs(),
                window.maxStringChars()))
        .name("session-precompute")
        .uid("session-precompute");
  }

  private static SingleOutputStreamOperator<ViewEventDocument> buildViewStream(
      DataStream<RumEvent> events, RumJobConfig.WindowConfig window) {
    // 仅带 view.id 的 span 进入该分支；view/other 事件虽不计数，仍会刷新活动 gap。
    return events
        .filter(ViewProcessFunction::hasViewId)
        .name("filter-view-events")
        .uid("filter-view-events")
        .keyBy(ViewProcessFunction::viewKeyOf)
        .process(
            new ViewProcessFunction(
                window.emitMs(), window.gapMs(), window.maxLifeMs(), window.stateTtlMs()))
        .name("view-precompute")
        .uid("view-precompute");
  }

  /** Kafka 启动位点:latest / earliest / committed 三选一,缺省 latest。 */
  private static OffsetsInitializer startingOffsets(RumJobConfig.StartupMode startupMode) {
    if (startupMode == RumJobConfig.StartupMode.EARLIEST) {
      return OffsetsInitializer.earliest();
    }
    if (startupMode == RumJobConfig.StartupMode.COMMITTED) {
      return OffsetsInitializer.committedOffsets(OffsetResetStrategy.EARLIEST);
    }
    return OffsetsInitializer.latest();
  }

  private static void writeSessionSink(
      RumJobConfig config, SingleOutputStreamOperator<SessionEvent> sessions) {
    if (config.sinkMode() == RumJobConfig.SinkMode.ELASTICSEARCH) {
      sessions
          .sinkTo(PrecalculateElasticsearchSink.forSession(config.rawParameters()))
          .name("session-precalculate-es-sink")
          .uid("session-precalculate-es-sink");
      return;
    }
    sessions.print("session-precalculate");
  }

  private static void writeLateEventSink(
      RumJobConfig config, DataStream<RumEvent> lateEvents) {
    if (config.lateSinkMode() == RumJobConfig.LateSinkMode.KAFKA) {
      KafkaSink<RumEvent> sink =
          KafkaSink.<RumEvent>builder()
              .setBootstrapServers(config.lateKafkaBootstrapServers())
              .setRecordSerializer(
                  KafkaRecordSerializationSchema.<RumEvent>builder()
                      .setTopic(config.lateKafkaTopic())
                      .setValueSerializationSchema(new RumEventSerializationSchema())
                      .build())
              .setDeliveryGuarantee(DeliveryGuarantee.AT_LEAST_ONCE)
              .build();
      lateEvents.sinkTo(sink).name("late-events-kafka-sink").uid("late-events-kafka-sink");
      return;
    }
    if (config.lateSinkMode() == RumJobConfig.LateSinkMode.STDOUT) {
      lateEvents.print("late-rum-events");
      return;
    }
  }

  private static void writeViewSink(
      RumJobConfig config, SingleOutputStreamOperator<ViewEventDocument> views) {
    if (config.sinkMode() == RumJobConfig.SinkMode.ELASTICSEARCH) {
      views
          .sinkTo(PrecalculateElasticsearchSink.forView(config.rawParameters()))
          .name("view-precalculate-es-sink")
          .uid("view-precalculate-es-sink");
      return;
    }
    views.print("view-precalculate");
  }
}
