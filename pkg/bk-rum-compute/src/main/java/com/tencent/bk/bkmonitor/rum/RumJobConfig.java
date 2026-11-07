// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum;

import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.Locale;
import javax.annotation.Nullable;
import org.apache.flink.util.ParameterTool;

/**
 * 经过归一化和校验的 RUM 作业运行配置。
 *
 * <p>{@link ParameterTool} 只在边界处解析一次，业务拓扑不再散落默认值或字符串 key。ES 适配器仍可通过
 * {@link #rawParameters()} 读取自身的 {@code sink.es.*} 配置。
 */
public final class RumJobConfig {
  /** 作业名同时作为默认 Kafka consumer group，便于按作业追踪消费位点。 */
  public static final String DEFAULT_JOB_NAME = "rum-session-precompute";

  /** KafkaSource 默认每 5 分钟发现一次新增 partition；在 builder 中显式传递，避免依赖隐式默认值。 */
  private static final long DEFAULT_PARTITION_DISCOVERY_INTERVAL_MS = 300_000L;
  private static final long DEFAULT_WINDOW_GAP_MINUTES = 15L;
  private static final long DEFAULT_WINDOW_MAX_LIFE_MINUTES = 240L;
  private static final long DEFAULT_SESSION_RETENTION_MINUTES = 120L;
  private static final long DEFAULT_VIEW_RETENTION_MINUTES = 120L;

  /** Kafka Source 初始 offset 策略；用 enum 防止拓扑层继续比较原始字符串。 */
  public enum StartupMode {
    LATEST,
    EARLIEST,
    COMMITTED
  }

  /** Session/View 主输出目标。 */
  public enum SinkMode {
    ELASTICSEARCH,
    STDOUT
  }

  /** 迟到事件可独立选择 Kafka、丢弃或调试输出。 */
  public enum LateSinkMode {
    KAFKA,
    NONE,
    STDOUT
  }

  /** 仅供 ES sink 等基础设施适配器读取自身的扩展参数，不应在拓扑中直接使用。 */
  private final ParameterTool rawParameters;
  private final String jobName;
  private final int parallelism;
  private final CheckpointConfig checkpoint;
  private final KafkaConfig kafka;
  private final WatermarkConfig watermark;
  private final WindowConfig sessionWindow;
  private final WindowConfig viewWindow;
  private final SinkMode sinkMode;
  private final LateSinkMode lateSinkMode;
  private final String lateKafkaBootstrapServers;
  private final String lateKafkaTopic;
  private final ShutdownConfig shutdown;

  private RumJobConfig(
      ParameterTool rawParameters,
      String jobName,
      int parallelism,
      CheckpointConfig checkpoint,
      KafkaConfig kafka,
      WatermarkConfig watermark,
      WindowConfig sessionWindow,
      WindowConfig viewWindow,
      SinkMode sinkMode,
      LateSinkMode lateSinkMode,
      String lateKafkaBootstrapServers,
      String lateKafkaTopic,
      ShutdownConfig shutdown) {
    this.rawParameters = rawParameters;
    this.jobName = jobName;
    this.parallelism = parallelism;
    this.checkpoint = checkpoint;
    this.kafka = kafka;
    this.watermark = watermark;
    this.sessionWindow = sessionWindow;
    this.viewWindow = viewWindow;
    this.sinkMode = sinkMode;
    this.lateSinkMode = lateSinkMode;
    this.lateKafkaBootstrapServers = lateKafkaBootstrapServers;
    this.lateKafkaTopic = lateKafkaTopic;
    this.shutdown = shutdown;
  }

  /**
   * 把命令行参数转换为不可变配置。
   *
   * <p>所有必填项、枚举值和数值边界都在此处一次校验，避免作业已提交后才在某个算子中失败。
   */
  public static RumJobConfig from(ParameterTool parameters) {
    // Source 的 broker 和 topic 没有可安全推断的默认值，因此必须显式传入。
    String bootstrapServers = requireNonBlank(parameters, "kafka.bootstrap.servers");
    String topic = requireNonBlank(parameters, "kafka.topic");
    String groupId = nonBlank(parameters.get("kafka.group.id", DEFAULT_JOB_NAME), "kafka.group.id");

    // fetch 参数在这里转换为数值，可在创建 KafkaSource 前拒绝 0/负数。
    KafkaConfig kafka =
        new KafkaConfig(
            bootstrapServers,
            topic,
            groupId,
            startupMode(parameters.get("kafka.startup.mode", "latest")),
            positiveLong(parameters, "kafka.fetch.min.bytes", 1_048_576L),
            positiveLong(parameters, "kafka.fetch.max.wait.ms", 500L),
            positiveLong(parameters, "kafka.max.partition.fetch.bytes", 16_777_216L),
            positiveInt(parameters, "kafka.max.poll.records", 5_000),
            positiveInt(parameters, "kafka.receive.buffer.bytes", 262_144),
            positiveLong(parameters, "kafka.connections.max.idle.ms", 540_000L),
            positiveLong(
                parameters,
                "kafka.partition.discovery.interval.ms",
                DEFAULT_PARTITION_DISCOVERY_INTERVAL_MS));

    // interval/timeout 必须为正；容忍次数和最小间隔允许显式设为 0。
    CheckpointConfig checkpoint =
        new CheckpointConfig(
            positiveLong(parameters, "checkpoint.interval.ms", TimeUtils.MINUTE_TO_MS),
            positiveLong(parameters, "checkpoint.timeout.ms", 600_000L),
            nonNegativeInt(parameters, "checkpoint.tolerable-failures", 3),
            nonNegativeLong(parameters, "checkpoint.min-pause.ms", 30_000L));

    // 活动窗口时长与关闭后的修正保留期分别控制，避免生命周期配置扩大逐事件状态的保留时间。
    WindowConfig sessionWindow =
        windowConfig(
            "session",
            positiveLong(parameters, "session.emit.interval.seconds", 30L),
            positiveLong(parameters, "session.gap.minutes", DEFAULT_WINDOW_GAP_MINUTES),
            positiveLong(parameters, "session.max.life.minutes", DEFAULT_WINDOW_MAX_LIFE_MINUTES),
            positiveLong(parameters, "session.state.ttl.minutes", DEFAULT_SESSION_RETENTION_MINUTES),
            positiveInt(parameters, "session.max.string.chars", 512));

    WindowConfig viewWindow =
        windowConfig(
            "view",
            positiveLong(parameters, "view.emit.interval.seconds", 30L),
            positiveLong(parameters, "view.gap.minutes", DEFAULT_WINDOW_GAP_MINUTES),
            positiveLong(parameters, "view.max.life.minutes", DEFAULT_WINDOW_MAX_LIFE_MINUTES),
            positiveLong(parameters, "view.state.ttl.minutes", DEFAULT_VIEW_RETENTION_MINUTES),
            512);

    // 迟到流可以使用独立 Kafka 集群；未配置时回退到主 Source broker。
    LateSinkMode lateSinkMode = lateSinkMode(parameters.get("late.sink.type", "kafka"));
    String lateBootstrapServers =
        nonBlank(
            parameters.get("late.kafka.bootstrap.servers", bootstrapServers),
            "late.kafka.bootstrap.servers");
    String lateTopic =
        nonBlank(parameters.get("late.kafka.topic", "rum-events-late"), "late.kafka.topic");

    return new RumJobConfig(
        parameters,
        nonBlank(parameters.get("job.name", DEFAULT_JOB_NAME), "job.name"),
        positiveInt(parameters, "parallelism", 4),
        checkpoint,
        kafka,
        new WatermarkConfig(
            nonNegativeLong(parameters, "watermark.out-of-orderness.seconds", 120L),
            positiveLong(parameters, "watermark.idle.minutes", 1L)),
        sessionWindow,
        viewWindow,
        sinkMode(parameters.get("sink.type", "es")),
        lateSinkMode,
        lateBootstrapServers,
        lateTopic,
        new ShutdownConfig(
            positiveLong(parameters, "shutdown.timeout.seconds", 30L),
            trimToNull(parameters.get("shutdown.savepoint.path", null))));
  }

  /**
   * 归一化窗口时间单位；活动窗口和关闭后的保留期拥有独立的时钟。
   *
   * <p>{@code stateTtlMinutes} 当前作为首次关窗后的显式 cleanup 保留期。
   * 该保留期从首次关闭开始计算，无需大于 gap 或 max-life。
   */
  private static WindowConfig windowConfig(
      String name,
      long emitIntervalSeconds,
      long gapMinutes,
      long maxLifeMinutes,
      long stateTtlMinutes,
      int maxStringChars) {
    // maxLife 必须严格大于 gap，否则窗口会在收到第一条事件后立即被 max-life timer 关闭，所有事件被丢弃。
    if (maxLifeMinutes <= gapMinutes) {
      throw new IllegalArgumentException(
          name
              + ".max.life.minutes ("
              + maxLifeMinutes
              + ") must be greater than "
              + name
              + ".gap.minutes ("
              + gapMinutes
              + ")");
    }
    long emitMs =
        multiplyExact(name + ".emit.interval.seconds", emitIntervalSeconds, TimeUtils.SECOND_TO_MS);
    long gapMs = multiplyExact(name + ".gap.minutes", gapMinutes, TimeUtils.MINUTE_TO_MS);
    long maxLifeMs =
        multiplyExact(name + ".max.life.minutes", maxLifeMinutes, TimeUtils.MINUTE_TO_MS);
    long stateTtlMs =
        multiplyExact(name + ".state.ttl.minutes", stateTtlMinutes, TimeUtils.MINUTE_TO_MS);
    return new WindowConfig(
        emitIntervalSeconds,
        gapMinutes,
        maxLifeMinutes,
        stateTtlMinutes,
        emitMs,
        gapMs,
        maxLifeMs,
        stateTtlMs,
        maxStringChars);
  }

  /** 严格解析启动模式，拼写错误不再静默退化为 latest。 */
  private static StartupMode startupMode(String raw) {
    String normalized = normalized(raw);
    if ("latest".equals(normalized)) {
      return StartupMode.LATEST;
    }
    if ("earliest".equals(normalized)) {
      return StartupMode.EARLIEST;
    }
    if ("committed".equals(normalized)) {
      return StartupMode.COMMITTED;
    }
    throw new IllegalArgumentException(
        "kafka.startup.mode must be one of latest, earliest, committed: " + raw);
  }

  /** 主 sink 只接受文档中公开的值，防止配置错误意外切到 stdout。 */
  private static SinkMode sinkMode(String raw) {
    String normalized = normalized(raw);
    if ("es".equals(normalized) || "elasticsearch".equals(normalized)) {
      return SinkMode.ELASTICSEARCH;
    }
    if ("stdout".equals(normalized)) {
      return SinkMode.STDOUT;
    }
    throw new IllegalArgumentException("sink.type must be one of es, elasticsearch, stdout: " + raw);
  }

  private static LateSinkMode lateSinkMode(String raw) {
    String normalized = normalized(raw);
    if ("kafka".equals(normalized)) {
      return LateSinkMode.KAFKA;
    }
    if ("none".equals(normalized)) {
      return LateSinkMode.NONE;
    }
    if ("stdout".equals(normalized)) {
      return LateSinkMode.STDOUT;
    }
    throw new IllegalArgumentException("late.sink.type must be one of kafka, none, stdout: " + raw);
  }

  private static String normalized(@Nullable String value) {
    return value == null ? "" : value.trim().toLowerCase(Locale.ROOT);
  }

  static String requireNonBlank(ParameterTool parameters, String key) {
    return nonBlank(parameters.get(key), key);
  }

  private static String nonBlank(String value, String key) {
    if (value == null || value.trim().isEmpty() || "__NO_VALUE_KEY".equals(value)) {
      throw new IllegalArgumentException("Missing or blank program argument --" + key + " <value>");
    }
    return value.trim();
  }

  @Nullable
  private static String trimToNull(@Nullable String value) {
    if (value == null || value.trim().isEmpty()) {
      return null;
    }
    return value.trim();
  }

  private static int positiveInt(ParameterTool parameters, String key, int defaultValue) {
    int value = parameters.getInt(key, defaultValue);
    if (value <= 0) {
      throw new IllegalArgumentException(key + " must be positive: " + value);
    }
    return value;
  }

  private static int nonNegativeInt(ParameterTool parameters, String key, int defaultValue) {
    int value = parameters.getInt(key, defaultValue);
    if (value < 0) {
      throw new IllegalArgumentException(key + " must not be negative: " + value);
    }
    return value;
  }

  private static long positiveLong(ParameterTool parameters, String key, long defaultValue) {
    long value = parameters.getLong(key, defaultValue);
    if (value <= 0L) {
      throw new IllegalArgumentException(key + " must be positive: " + value);
    }
    return value;
  }

  private static long nonNegativeLong(ParameterTool parameters, String key, long defaultValue) {
    long value = parameters.getLong(key, defaultValue);
    if (value < 0L) {
      throw new IllegalArgumentException(key + " must not be negative: " + value);
    }
    return value;
  }

  /** 使用精确乘法转毫秒，避免极端参数溢出后变成负 timer。 */
  private static long multiplyExact(String key, long value, long multiplier) {
    try {
      return Math.multiplyExact(value, multiplier);
    } catch (ArithmeticException e) {
      throw new IllegalArgumentException(key + " is too large: " + value, e);
    }
  }

  public ParameterTool rawParameters() {
    return rawParameters;
  }

  public String jobName() {
    return jobName;
  }

  public int parallelism() {
    return parallelism;
  }

  public CheckpointConfig checkpoint() {
    return checkpoint;
  }

  public KafkaConfig kafka() {
    return kafka;
  }

  public WatermarkConfig watermark() {
    return watermark;
  }

  public WindowConfig sessionWindow() {
    return sessionWindow;
  }

  public WindowConfig viewWindow() {
    return viewWindow;
  }

  public SinkMode sinkMode() {
    return sinkMode;
  }

  public LateSinkMode lateSinkMode() {
    return lateSinkMode;
  }

  public String lateKafkaBootstrapServers() {
    return lateKafkaBootstrapServers;
  }

  public String lateKafkaTopic() {
    return lateKafkaTopic;
  }

  public ShutdownConfig shutdown() {
    return shutdown;
  }

  /** 已校验的 checkpoint 调度参数。 */
  public static final class CheckpointConfig {
    private final long intervalMs;
    private final long timeoutMs;
    private final int tolerableFailures;
    private final long minPauseMs;

    private CheckpointConfig(
        long intervalMs, long timeoutMs, int tolerableFailures, long minPauseMs) {
      this.intervalMs = intervalMs;
      this.timeoutMs = timeoutMs;
      this.tolerableFailures = tolerableFailures;
      this.minPauseMs = minPauseMs;
    }

    public long intervalMs() {
      return intervalMs;
    }

    public long timeoutMs() {
      return timeoutMs;
    }

    public int tolerableFailures() {
      return tolerableFailures;
    }

    public long minPauseMs() {
      return minPauseMs;
    }
  }

  /** Kafka Source 连接、位点和攒批配置。 */
  public static final class KafkaConfig {
    private final String bootstrapServers;
    private final String topic;
    private final String groupId;
    private final StartupMode startupMode;
    private final long fetchMinBytes;
    private final long fetchMaxWaitMs;
    private final long maxPartitionFetchBytes;
    private final int maxPollRecords;
    private final int receiveBufferBytes;
    private final long connectionsMaxIdleMs;
    private final long partitionDiscoveryIntervalMs;

    private KafkaConfig(
        String bootstrapServers,
        String topic,
        String groupId,
        StartupMode startupMode,
        long fetchMinBytes,
        long fetchMaxWaitMs,
        long maxPartitionFetchBytes,
        int maxPollRecords,
        int receiveBufferBytes,
        long connectionsMaxIdleMs,
        long partitionDiscoveryIntervalMs) {
      this.bootstrapServers = bootstrapServers;
      this.topic = topic;
      this.groupId = groupId;
      this.startupMode = startupMode;
      this.fetchMinBytes = fetchMinBytes;
      this.fetchMaxWaitMs = fetchMaxWaitMs;
      this.maxPartitionFetchBytes = maxPartitionFetchBytes;
      this.maxPollRecords = maxPollRecords;
      this.receiveBufferBytes = receiveBufferBytes;
      this.connectionsMaxIdleMs = connectionsMaxIdleMs;
      this.partitionDiscoveryIntervalMs = partitionDiscoveryIntervalMs;
    }

    public String bootstrapServers() {
      return bootstrapServers;
    }

    public String topic() {
      return topic;
    }

    public String groupId() {
      return groupId;
    }

    public StartupMode startupMode() {
      return startupMode;
    }

    public long fetchMinBytes() {
      return fetchMinBytes;
    }

    public long fetchMaxWaitMs() {
      return fetchMaxWaitMs;
    }

    public long maxPartitionFetchBytes() {
      return maxPartitionFetchBytes;
    }

    public int maxPollRecords() {
      return maxPollRecords;
    }

    public int receiveBufferBytes() {
      return receiveBufferBytes;
    }

    public long connectionsMaxIdleMs() {
      return connectionsMaxIdleMs;
    }

    public long partitionDiscoveryIntervalMs() {
      return partitionDiscoveryIntervalMs;
    }
  }

  /** 事件时间乱序界限与空闲 partition 检测配置。 */
  public static final class WatermarkConfig {
    private final long outOfOrdernessSeconds;
    private final long idleMinutes;

    private WatermarkConfig(long outOfOrdernessSeconds, long idleMinutes) {
      this.outOfOrdernessSeconds = outOfOrdernessSeconds;
      this.idleMinutes = idleMinutes;
    }

    public long outOfOrdernessSeconds() {
      return outOfOrdernessSeconds;
    }

    public long idleMinutes() {
      return idleMinutes;
    }
  }

  /**
   * Session/View 共用的处理时间窗口配置。
   *
   * <p>同时保留用户可读单位（秒/分钟）与算子使用的毫秒，启动日志无需反向换算。
   */
  public static final class WindowConfig {
    private final long emitIntervalSeconds;
    private final long gapMinutes;
    private final long maxLifeMinutes;
    private final long stateTtlMinutes;
    private final long emitMs;
    private final long gapMs;
    private final long maxLifeMs;
    private final long stateTtlMs;
    private final int maxStringChars;

    private WindowConfig(
        long emitIntervalSeconds,
        long gapMinutes,
        long maxLifeMinutes,
        long stateTtlMinutes,
        long emitMs,
        long gapMs,
        long maxLifeMs,
        long stateTtlMs,
        int maxStringChars) {
      this.emitIntervalSeconds = emitIntervalSeconds;
      this.gapMinutes = gapMinutes;
      this.maxLifeMinutes = maxLifeMinutes;
      this.stateTtlMinutes = stateTtlMinutes;
      this.emitMs = emitMs;
      this.gapMs = gapMs;
      this.maxLifeMs = maxLifeMs;
      this.stateTtlMs = stateTtlMs;
      this.maxStringChars = maxStringChars;
    }

    public long emitIntervalSeconds() {
      return emitIntervalSeconds;
    }

    public long gapMinutes() {
      return gapMinutes;
    }

    public long maxLifeMinutes() {
      return maxLifeMinutes;
    }

    public long stateTtlMinutes() {
      return stateTtlMinutes;
    }

    public long emitMs() {
      return emitMs;
    }

    public long gapMs() {
      return gapMs;
    }

    public long maxLifeMs() {
      return maxLifeMs;
    }

    public long stateTtlMs() {
      return stateTtlMs;
    }

    public int maxStringChars() {
      return maxStringChars;
    }
  }

  /** 附着式客户端的优雅停机超时和可选 savepoint 路径。 */
  public static final class ShutdownConfig {
    private final long timeoutSeconds;
    @Nullable
    private final String savepointPath;

    private ShutdownConfig(long timeoutSeconds, @Nullable String savepointPath) {
      this.timeoutSeconds = timeoutSeconds;
      this.savepointPath = savepointPath;
    }

    public long timeoutSeconds() {
      return timeoutSeconds;
    }

    @Nullable
    public String savepointPath() {
      return savepointPath;
    }
  }
}
