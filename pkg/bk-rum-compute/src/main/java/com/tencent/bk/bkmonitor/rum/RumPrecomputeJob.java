// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import javax.annotation.Nullable;
import org.apache.flink.core.execution.CheckpointingMode;
import org.apache.flink.core.execution.JobClient;
import org.apache.flink.core.execution.SavepointFormatType;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.flink.util.ParameterTool;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * RUM 会话预计算作业入口。
 *
 * <p>数据链路:
 *
 * <pre>
 *   Kafka(rum-events) -> watermark -> 过滤非法事件 -> keyBy(sessionId)
 *      -> SessionizeProcessFunction(事件时间会话聚合)
 *      -> 主输出:SessionSummary -> ES / stdout
 *      -> side output:迟到事件 -> Kafka / stdout / 丢弃
 * </pre>
 *
 * <p>所有运行参数通过命令行 {@code --key value} 传入,默认值与说明见 README「关键参数」。
 */
public final class RumPrecomputeJob {
  private static final Logger LOG = LoggerFactory.getLogger(RumPrecomputeJob.class);

  private RumPrecomputeJob() {}

  public static void main(String[] args) throws Exception {
    // Flink Web UI 可能把整段 Program Arguments 作为单个 argv 传入，先归一化再交给 ParameterTool。
    String[] normalizedArgs = normalizeProgramArgs(args);
    ParameterTool parameters = loadParameters(normalizedArgs);
    logParsedParameters(args, normalizedArgs, parameters);
    // 在创建 Source/Sink 前 fail fast，避免无效配置变成集群中的异步作业失败。
    RumJobConfig config = RumJobConfig.from(parameters);
    StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment();
    // 全局参数,供各算子在 open() 时通过 getRuntimeContext().getGlobalJobParameters() 读取。
    env.getConfig().setGlobalJobParameters(parameters);
    // 上下游算子间复用同一对象引用,降低 GC 压力。
    // 前置条件:下游算子只读 input event,不可改写。当前 Sessionize/View 两个 ProcessFunction
    // 以及后续 ES sink 都满足该条件。
    env.getConfig().enableObjectReuse();
    env.setParallelism(config.parallelism());
    // Checkpoint 调度由作业级强类型参数统一管理，集群配置只负责后端、存储和增量策略。
    env.enableCheckpointing(
        config.checkpoint().intervalMs(), CheckpointingMode.EXACTLY_ONCE);
    env.getCheckpointConfig().setCheckpointTimeout(config.checkpoint().timeoutMs());
    // 允许连续 N 次 checkpoint 失败而不触发作业失败(默认 0 = 零容忍)
    env.getCheckpointConfig()
        .setTolerableCheckpointFailureNumber(config.checkpoint().tolerableFailures());
    // 两次 checkpoint 之间的最小间隔,避免 checkpoint 风暴
    env.getCheckpointConfig()
        .setMinPauseBetweenCheckpoints(config.checkpoint().minPauseMs());

    // 启动时打印关键运行参数,便于确认作业配置;一次性输出,生产环境可保留 INFO。
    // 以已归一化的强类型配置记录实际运行值，避免日志与拓扑默认值漂移。
    LOG.info(
        "starting rum precompute job: jobName={}, parallelism={}, checkpointIntervalMs={},"
            + " checkpointTimeoutMs={}, checkpointTolerableFailures={}, checkpointMinPauseMs={},"
            + " sessionEmitIntervalSeconds={}, sessionGapMinutes={},"
            + " sessionMaxLifeMinutes={}, sessionStateTtlMinutes={}, kafkaBootstrapServers={},"
            + " kafkaTopic={}, kafkaGroupId={}, kafkaStartupMode={},"
            + " watermarkOutOfOrdernessSeconds={}, watermarkIdleMinutes={},"
            + " viewEmitIntervalSeconds={}, viewGapMinutes={}, viewMaxLifeMinutes={},"
            + " viewStateTtlMinutes={},"
            + " sinkType={}, lateSinkType={},"
            + " shutdownTimeoutSeconds={}, shutdownSavepointPath={}",
        config.jobName(),
        config.parallelism(),
        config.checkpoint().intervalMs(),
        config.checkpoint().timeoutMs(),
        config.checkpoint().tolerableFailures(),
        config.checkpoint().minPauseMs(),
        config.sessionWindow().emitIntervalSeconds(),
        config.sessionWindow().gapMinutes(),
        config.sessionWindow().maxLifeMinutes(),
        config.sessionWindow().stateTtlMinutes(),
        config.kafka().bootstrapServers(),
        config.kafka().topic(),
        config.kafka().groupId(),
        config.kafka().startupMode().name().toLowerCase(Locale.ROOT),
        config.watermark().outOfOrdernessSeconds(),
        config.watermark().idleMinutes(),
        config.viewWindow().emitIntervalSeconds(),
        config.viewWindow().gapMinutes(),
        config.viewWindow().maxLifeMinutes(),
        config.viewWindow().stateTtlMinutes(),
        config.sinkMode().name().toLowerCase(Locale.ROOT),
        config.lateSinkMode().name().toLowerCase(Locale.ROOT),
        config.shutdown().timeoutSeconds(),
        config.shutdown().savepointPath());

    // 拓扑装配与进程生命周期分层；现有 operator UID 保持不变，可继续从原 savepoint 恢复。
    RumJobTopology.build(env, config);

    // ---- 6. 提交作业并注册优雅关闭 ----
    // executeAsync 拿到 JobClient(非阻塞),再注册 JVM 关闭钩子:收到 SIGTERM(Ctrl+C / kill /
    // 容器停止)时优雅停止作业,而不是硬杀导致 ES sink in-flight bulk 丢失。详见 registerGracefulShutdown。
    String jobName = config.jobName();
    JobClient jobClient = env.executeAsync(jobName);
    if (isWebSubmissionJobClient(jobClient)) {
      LOG.info(
          "rum precompute job submitted through Flink Web UI: jobName={}, jobId={}",
          jobName,
          jobClient.getJobID());
      return;
    }

    AtomicBoolean gracefulShutdownInitiated = new AtomicBoolean(false);
    registerGracefulShutdown(jobClient, config.shutdown(), gracefulShutdownInitiated);

    try {
      jobClient.getJobExecutionResult().get();
    } catch (Exception e) {
      // 钩子触发的取消会让 get() 抛异常,属正常退出;只有非主动取消的异常才算作业失败。
      if (gracefulShutdownInitiated.get()) {
        LOG.info("rum precompute job stopped gracefully: jobName={}", jobName);
      } else {
        LOG.error("rum precompute job terminated abnormally: jobName={}", jobName, e);
        throw e;
      }
    }
  }

  /**
   * 加载启动参数：先按需读取 {@code --config <path>} 指定的 JSON 文件，再用命令行参数覆盖其中的同名 key。
   *
   * <p>JSON 文件格式：顶层必须是 object，每个字段都是字符串，例如：
   *
   * <pre>{@code
   * {
   *   "kafka.bootstrap.servers": "localhost:9092",
   *   "kafka.topic": "rum-events",
   *   "sink.type": "es"
   * }
   * }</pre>
   *
   * <p>仅支持扁平结构，嵌套对象/数组会在加载阶段报错，避免 key 命名规范分裂。命令行中显式传入的同名参数始终覆盖 JSON 文件中的值，方便临时调整。
   */
  static ParameterTool loadParameters(String[] normalizedArgs) {
    SplitArgs split = extractConfigPath(normalizedArgs);
    ParameterTool commandLine = ParameterTool.fromArgs(split.remainingArgs);
    if (split.configPath == null) {
      return commandLine;
    }
    ParameterTool fromFile = readJsonConfig(split.configPath);
    // mergeWith(other) 中 other 覆盖 this：让命令行覆盖 JSON 文件。
    return fromFile.mergeWith(commandLine);
  }

  /**
   * 把 {@code --config <path>} 单独摘出来，剩余参数交给 {@link ParameterTool#fromArgs(String[])} 解析。
   *
   * <p>只识别 {@code --config <value>} 这种主参数形式；JSON 文件内部仍以同样的 key 提供配置。
   */
  private static SplitArgs extractConfigPath(String[] args) {
    String configPath = null;
    List<String> remaining = new ArrayList<>(args.length);
    for (int i = 0; i < args.length; i++) {
      if ("--config".equals(args[i]) && i + 1 < args.length) {
        configPath = args[++i];
      } else {
        remaining.add(args[i]);
      }
    }
    return new SplitArgs(configPath, remaining.toArray(new String[0]));
  }

  /** 读取并校验 JSON 配置文件；任何读取/解析错误都包装为 {@link IllegalArgumentException}。 */
  private static ParameterTool readJsonConfig(String path) {
    Path file = Paths.get(path);
    if (!Files.exists(file)) {
      throw new IllegalArgumentException(
          "JSON config file not found: " + file.toAbsolutePath());
    }
    ObjectMapper mapper = new ObjectMapper();
    JsonNode root;
    try (InputStream in = Files.newInputStream(file)) {
      root = mapper.readTree(in);
    } catch (IOException e) {
      throw new IllegalArgumentException(
          "failed to read JSON config file -- " + file.toAbsolutePath() + ": " + e.getMessage(),
          e);
    }
    if (root == null || root.isMissingNode() || root.isNull()) {
      throw new IllegalArgumentException(
          "JSON config file is empty: " + file.toAbsolutePath());
    }
    if (!root.isObject()) {
      throw new IllegalArgumentException(
          "JSON config file top-level must be an object: " + file.toAbsolutePath());
    }
    Map<String, String> values = new HashMap<>();
    Iterator<Map.Entry<String, JsonNode>> fields = root.fields();
    while (fields.hasNext()) {
      Map.Entry<String, JsonNode> entry = fields.next();
      JsonNode value = entry.getValue();
      if (!value.isTextual()) {
        throw new IllegalArgumentException(
            "JSON config field '"
                + entry.getKey()
                + "' must be a string; nested structures are not supported (file: "
                + file.toAbsolutePath()
                + ")");
      }
      values.put(entry.getKey(), value.asText());
    }
    return ParameterTool.fromMap(values);
  }

  private static final class SplitArgs {
    @Nullable
    private final String configPath;
    private final String[] remainingArgs;

    private SplitArgs(@Nullable String configPath, String[] remainingArgs) {
      this.configPath = configPath;
      this.remainingArgs = remainingArgs;
    }
  }

  private static String[] normalizeProgramArgs(String[] args) {
    if (args.length == 1 && args[0] != null && args[0].contains("--")) {
      String[] splitArgs = splitProgramArgumentString(args[0]);
      if (splitArgs.length > 1) {
        return splitArgs;
      }
    }
    return args;
  }

  private static String[] splitProgramArgumentString(String rawArgs) {
    List<String> result = new ArrayList<>();
    StringBuilder current = new StringBuilder();
    boolean inSingleQuote = false;
    boolean inDoubleQuote = false;
    boolean escaping = false;
    for (int i = 0; i < rawArgs.length(); i++) {
      char c = rawArgs.charAt(i);
      if (escaping) {
        current.append(c);
        escaping = false;
        continue;
      }
      if (c == '\\' && !inSingleQuote) {
        escaping = true;
        continue;
      }
      if (c == '\'' && !inDoubleQuote) {
        inSingleQuote = !inSingleQuote;
        continue;
      }
      if (c == '"' && !inSingleQuote) {
        inDoubleQuote = !inDoubleQuote;
        continue;
      }
      if (Character.isWhitespace(c) && !inSingleQuote && !inDoubleQuote) {
        appendArgumentIfPresent(result, current);
        continue;
      }
      current.append(c);
    }
    if (escaping) {
      current.append('\\');
    }
    appendArgumentIfPresent(result, current);
    return result.toArray(new String[0]);
  }

  private static void appendArgumentIfPresent(List<String> result, StringBuilder current) {
    if (current.length() > 0) {
      result.add(current.toString());
      current.setLength(0);
    }
  }

  private static void logParsedParameters(
      String[] originalArgs, String[] normalizedArgs, ParameterTool parameters) {
    LOG.info(
        "program arguments parsed: originalArgCount={}, normalizedArgCount={}, "
            + "parameterCount={}, parameters={}",
        originalArgs.length,
        normalizedArgs.length,
        parameters.getNumberOfParameters(),
        redactedParameters(parameters));

    if (!parameters.has("kafka.bootstrap.servers") || !parameters.has("kafka.topic")) {
      LOG.warn(
          "kafka source parameters were not provided explicitly; the job will fail fast. "
              + "For Flink Web UI submission, put --kafka.bootstrap.servers and --kafka.topic "
              + "in Program Arguments.");
    }
  }

  private static Map<String, String> redactedParameters(ParameterTool parameters) {
    Map<String, String> redacted = new TreeMap<>();
    for (Map.Entry<String, String> entry : parameters.toMap().entrySet()) {
      redacted.put(entry.getKey(), shouldRedact(entry.getKey()) ? "<redacted>" : entry.getValue());
    }
    return redacted;
  }

  private static boolean shouldRedact(String key) {
    String normalized = key.toLowerCase(Locale.ROOT);
    return normalized.contains("password")
        || normalized.contains("secret")
        || normalized.contains("token")
        || normalized.contains("credential")
        || normalized.contains("sasl.jaas.config");
  }

  private static boolean isWebSubmissionJobClient(JobClient jobClient) {
    return "org.apache.flink.client.deployment.application.WebSubmissionJobClient"
        .equals(jobClient.getClass().getName());
  }

  /**
   * 注册 JVM 关闭钩子,在 SIGTERM(Ctrl+C / {@code kill} / 容器停止)时优雅停止作业。
   *
   * <p>停止方式:
   *
   * <ul>
   *   <li>设了 {@code shutdown.savepoint.path} -> {@code
   *       stopWithSavepoint(advanceToEndOfEventTime=true)}: 排空整条链路后保存 savepoint,可从该点恢复,适合维护性停机;
   *   <li>否则 -> {@code cancel()}:立即停止 source,但算子 {@code close()} 仍会执行, ES sink 会刷掉 in-flight bulk。已
   *       checkpoint 的 Kafka offset 不丢,重启从最近 checkpoint 继续 (at-least-once + ES 幂等 upsert,无数据丢失)。
   * </ul>
   *
   * <p>钩子带 {@code shutdown.timeout.seconds} 超时,超时或失败仅打告警、不阻塞 JVM 退出 (最坏情况退化为硬杀,等同改动前行为)。{@code
   * gracefulShutdownInitiated} 由钩子置位, 供 {@code main} 区分「主动优雅停止」与「作业自身异常」。
   *
   * <p>注意:此机制仅对客户端附着运行({@code mvn exec:java} / mini-cluster)生效; 提交到独立集群用 {@code flink run -d} 后,请改用
   * {@code flink stop --savepointPath ... <jobId>}。
   */
  private static void registerGracefulShutdown(
      JobClient jobClient,
      RumJobConfig.ShutdownConfig shutdown,
      AtomicBoolean gracefulShutdownInitiated) {
    String savepointPath = shutdown.savepointPath();
    long timeoutSeconds = shutdown.timeoutSeconds();
    Thread hook =
        new Thread(
            () -> {
              gracefulShutdownInitiated.set(true);
              try {
                if (savepointPath != null && !savepointPath.trim().isEmpty()) {
                  LOG.info(
                      "graceful shutdown: stop with savepoint, path={}, jobId={}",
                      savepointPath,
                      jobClient.getJobID());
                  String path =
                      jobClient
                          .stopWithSavepoint(true, savepointPath, SavepointFormatType.CANONICAL)
                          .get(timeoutSeconds, TimeUnit.SECONDS);
                  LOG.info("graceful shutdown: savepoint created at {}", path);
                } else {
                  LOG.info("graceful shutdown: cancel job, jobId={}", jobClient.getJobID());
                  jobClient.cancel().get(timeoutSeconds, TimeUnit.SECONDS);
                  LOG.info("graceful shutdown: job cancelled cleanly");
                }
              } catch (Throwable t) {
                LOG.warn(
                    "graceful shutdown did not complete within {}s or failed: {}",
                    timeoutSeconds,
                    t.toString());
              }
            },
            "flink-rum-graceful-shutdown");
    Runtime.getRuntime().addShutdownHook(hook);
  }

}
