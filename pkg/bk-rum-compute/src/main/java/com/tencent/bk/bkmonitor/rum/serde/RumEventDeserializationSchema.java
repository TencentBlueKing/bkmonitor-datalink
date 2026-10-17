// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.serde;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.Span;
import com.tencent.bk.bkmonitor.rum.utils.SpanAttributeUtils;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.ObjectWriter;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.io.IOException;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.List;
import javax.annotation.Nullable;
import org.apache.flink.api.common.serialization.DeserializationSchema;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.connector.kafka.source.reader.deserializer.KafkaRecordDeserializationSchema;
import org.apache.flink.metrics.Counter;
import org.apache.flink.metrics.MetricGroup;
import org.apache.flink.util.Collector;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 把 Kafka {@code rum-events} topic 的消息反序列化成 {@link RumEvent}。
 *
 * <p>同时实现两个接口:
 *
 * <ul>
 *   <li>{@link KafkaRecordDeserializationSchema}:作业实际使用的方式,一条 Kafka record 通过 {@link
 *       #deserialize(ConsumerRecord, Collector)} 展开成多条 RumEvent(因为 采集网关 envelope 的 {@code items[]}
 *       里可能有多条 span);
 *   <li>{@link DeserializationSchema}:兼容 SourceFunction 等单条场景,取展开后的第一条。
 * </ul>
 *
 * <p>支持两种 JSON 形态:
 *
 * <ol>
 *   <li>采集网关 envelope(生产形态):真实 span 在 {@code items[]} 内,字段映射见 README;
 *   <li>早期 flat event(本地压测/回放):字段平铺在根节点,见 {@link #fromFlatEvent}。
 * </ol>
 *
 * <p>所有字段读取都通过「候选字段名列表」查找,容忍驼峰/下划线等多种命名。 时间字段默认按微秒(Kafka 上游口径)处理,同时兼容秒/毫秒/纳秒,见 {@link #normalizeEpochMicros}。
 */
public class RumEventDeserializationSchema
    implements DeserializationSchema<RumEvent>, KafkaRecordDeserializationSchema<RumEvent> {
  private static final long serialVersionUID = 1L;

  /** envelope 里 {@code datetime}/{@code utctime} 使用的「yyyy-MM-dd HH:mm:ss」格式。 */
  private static final DateTimeFormatter SPACE_DATE_TIME =
      DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

  private static final String SESSION_ID = "session.id";
  private static final String SPAN_TYPE = "span_type";

  // ObjectMapper 自身线程安全,跨实例共享一份即可;关闭未知字段报错,保证 schema 演进时反序列化不中断。
  private static final ObjectMapper MAPPER =
      new ObjectMapper().configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);

  // debug 日志专用的紧凑 ObjectWriter,避免每条事件都重建 pretty writer。
  private static final ObjectWriter COMPACT_WRITER = MAPPER.writer();

  /** JSON 结构非法、root 类型错误或无法识别的消息数。 */
  private transient Counter malformedRecordCounter;

  /** envelope 中因非 object 或字段类型错误被跳过的 item 数。 */
  private transient Counter malformedItemCounter;

  /** 本地计数用于限制 warn 日志，避免持续坏数据引发日志风暴。 */
  private transient long malformedRecordCount;
  private transient long malformedItemCount;

  private static final Logger LOG = LoggerFactory.getLogger(RumEventDeserializationSchema.class);

  @Override
  public void open(InitializationContext context) {
    MetricGroup serdeMetrics = context.getMetricGroup().addGroup("serde");
    malformedRecordCounter = serdeMetrics.counter("malformed_records");
    malformedItemCounter = serdeMetrics.counter("malformed_items");
  }

  /**
   * 单条反序列化(兼容 {@link DeserializationSchema} 接口):取展开后的第一条事件, 空消息返回空 RumEvent。作业主链路走 {@link
   * #deserialize(ConsumerRecord, Collector)}。
   */
  @Override
  public RumEvent deserialize(@Nullable byte[] message) throws IOException {
    List<RumEvent> events = deserializeMany(message);
    return events.isEmpty() ? new RumEvent() : events.get(0);
  }

  /** Kafka record 反序列化:把一条 record 里所有 span 逐条 collect 出去。 */
  @Override
  public void deserialize(ConsumerRecord<byte[], byte[]> record, Collector<RumEvent> out)
      throws IOException {
    List<RumEvent> events = deserializeMany(record.value());
    for (RumEvent event : events) {
      // 打印每条消费到的 RUM 事件,便于本地观察原始数据;生产可调高日志级别关闭。
      // 先判 isDebugEnabled,避免 debug 关闭时每条事件仍做一次无意义的 JSON 序列化。
      // 使用紧凑 ObjectWriter(单行 JSON),比 pretty 模式快 3-5x。
      if (LOG.isDebugEnabled()) {
        try {
          LOG.debug("consumed rum event: {}", COMPACT_WRITER.writeValueAsString(event));
        } catch (IOException e) {
          // 日志失败不应影响主链路;落到下一条 warn 即可。
          LOG.warn("failed to serialize rum event for debug log", e);
        }
      }
      out.collect(event);
    }
  }

  /**
   * 解析一条 Kafka 消息,返回 0~N 条 RumEvent。
   *
   * <p>使用 streaming parser 扫描完整 root，因此 fallback 时间字段可出现在 {@code items[]}
   * 前或后。item 先读成独立 tree 再绑定为 {@link Span}，单个 item 的 schema 错误不会影响同一
   * Kafka record 内的其他 item。
   *
   * <p>若根节点含 {@code items[]} 数组(envelope 形态),逐个 item 解析; 否则视为早期 flat event,整条解析成一个事件。
   */
  public List<RumEvent> deserializeMany(@Nullable byte[] message) throws IOException {
    List<RumEvent> events = new ArrayList<>();
    if (message == null || message.length == 0) {
      markMalformedRecord(0, "empty message", null);
      return events;
    }
    try {
      deserializeValidJson(message, events);
    } catch (IOException | RuntimeException e) {
      // 单条坏 record 不应触发 Source 重启：记录指标后跳过，继续消费后续 offset。
      markMalformedRecord(message.length, "invalid JSON or record schema", e);
    }
    return events;
  }

  /** envelope 形态下需要从 root 透传的字段名;其它 root 字段直接 skip,不占用内存。 */
  private static final String[] ROOT_TIME_FIELDS = {"time", "timestamp", "ts", "utctime", "datetime"};
  private static final String[] ROOT_APP_NAME_FIELDS = {"app_name", "appName"};
  private static final String[] ROOT_BIZ_ID_FIELDS = {"bk_biz_id", "bkBizId", "bizid"};

  /** 解析结构合法的 JSON；语法异常由上层按 record 级隔离。 */
  private void deserializeValidJson(byte[] message, List<RumEvent> events) throws IOException {
    try (JsonParser p = MAPPER.createParser(message)) {
      if (p.nextToken() != JsonToken.START_OBJECT) {
        markMalformedRecord(message.length, "root is not a JSON object", null);
        return;
      }

      List<Span> spans = new ArrayList<>();
      boolean sawItems = false;
      boolean invalidItemsField = false;
      // envelope 路径只需要 3 个 root 字段；用 *Set 锁定「是否已经读到」，避免把 rootFallbackTime=0
      // 误判为「还没找到」。这些字段直接走 streaming 的标量读取，不构造中间 JsonNode 子树。
      long rootFallbackTime = 0L;
      String rootAppName = null;
      Integer rootBkBizId = null;
      boolean rootFallbackTimeSet = false;
      boolean rootAppNameSet = false;
      boolean rootBkBizIdSet = false;
      JsonToken token;
      while ((token = p.nextToken()) != JsonToken.END_OBJECT && token != null) {
        if (token != JsonToken.FIELD_NAME) {
          p.skipChildren();
          continue;
        }
        String field = p.currentName();
        JsonToken next = p.nextToken();
        if ("items".equals(field)) {
          sawItems = true;
          if (next == JsonToken.START_ARRAY) {
            readEnvelopeItems(p, spans);
          } else {
            invalidItemsField = true;
            p.skipChildren();
          }
          continue;
        }
        // envelope 路径只需要这几个 root 字段；其它字段直接 skip，不占内存。
        // 三个 helper 内部对复合 token 自行 skipChildren，保证 parser 始终推进。
        if (!rootFallbackTimeSet && contains(ROOT_TIME_FIELDS, field)) {
          rootFallbackTime = readScalarAsEpochMicros(p, next);
          rootFallbackTimeSet = true;
        } else if (!rootAppNameSet && contains(ROOT_APP_NAME_FIELDS, field)) {
          rootAppName = readScalarAsString(p, next);
          rootAppNameSet = true;
        } else if (!rootBkBizIdSet && contains(ROOT_BIZ_ID_FIELDS, field)) {
          rootBkBizId = readScalarAsInteger(p, next);
          rootBkBizIdSet = true;
        } else {
          p.skipChildren();
        }
      }

      if (sawItems) {
        if (invalidItemsField) {
          markMalformedRecord(message.length, "items field is not an array", null);
          return;
        }
        for (Span span : spans) {
          inheritEnvelopeDimensions(span, rootAppName, rootBkBizId);
          events.add(fromSpanEnvelopeItem(span, rootFallbackTime));
        }
        return;
      }

      // 非 envelope:flat event 是早期/回放路径,占比低,用第二个 parser 一次性完整解析,代码最简单。
      RumEvent flatEvent = fromFlatEvent(parseObjectNode(message));
      if (flatEvent == null) {
        markMalformedRecord(message.length, "unrecognized flat event", null);
        return;
      }
      events.add(flatEvent);
    }
  }

  /** flat event 路径用第二个 parser 完整解析整条 JSON;非生产主路径,可接受一次性 parse。 */
  private static ObjectNode parseObjectNode(byte[] message) throws IOException {
    return (ObjectNode) MAPPER.readTree(message);
  }

  private static boolean contains(String[] haystack, String needle) {
    for (String s : haystack) {
      if (s.equals(needle)) {
        return true;
      }
    }
    return false;
  }

  /** 读取 items[] 并隔离单个 item 的类型绑定失败。 */
  private void readEnvelopeItems(JsonParser p, List<Span> spans) throws IOException {
    int itemIndex = 0;
    JsonToken itemToken;
    while ((itemToken = p.nextToken()) != JsonToken.END_ARRAY && itemToken != null) {
      if (itemToken != JsonToken.START_OBJECT) {
        markMalformedItem(itemIndex, "item is not an object", null);
        p.skipChildren();
        itemIndex++;
        continue;
      }
      // 先消费完整 item，避免类型绑定失败后共享 parser 停在子对象内，破坏后续 item 边界。
      // JSON 语法错误仍属于整条消息的错误，不能作为可恢复的 item 类型错误吞掉。
      JsonNode item = MAPPER.readTree(p);
      try {
        spans.add(MAPPER.treeToValue(item, Span.class));
      } catch (IOException | RuntimeException e) {
        markMalformedItem(itemIndex, "item does not match span schema", e);
      }
      itemIndex++;
    }
  }

  /** item 缺少维度时继承 envelope 级 app/biz 字段。 */
  private static void inheritEnvelopeDimensions(
      Span span, String rootAppName, Integer rootBkBizId) {
    if (span.getAppName() == null) {
      span.setAppName(rootAppName);
    }
    if (span.getBkBizId() == null) {
      span.setBkBizId(rootBkBizId);
    }
  }

  @Override
  public boolean isEndOfStream(RumEvent nextElement) {
    return false;
  }

  @Override
  public TypeInformation<RumEvent> getProducedType() {
    return TypeInformation.of(RumEvent.class);
  }

  /**
   * 从 envelope 的单个 span item 解析出一条 RumEvent。
   *
   * <p>当前只绑定「span / resource / spanStatus / 事件时间 / sessionId / spanType」这 6 个字段;其它维度(env,
   * version, device, view.url, web vitals, error.* 等)保留在 {@link Span#getAttributes()} 内,供下游
   * {@code SessionEventAccumulator} / {@code ViewProcessFunction} 等算子按需提取。
   *
   * <p>几点行为约束:
   *
   * <ul>
   *   <li>不推导 eventType(网络错误/资源错误/page_view/click/resource 等分类):下游基于 {@code
   *       spanName} / {@code spanType} 字符串自行判断;
   *   <li>不拼接 event_id:需要时由消费者按 {@code traceId + ":" + spanId} 自行拼;
   *   <li>不计算 loadTime:页面加载耗时需结合 endTime - startTime,留给下游 view 算子处理。
   * </ul>
   */
  private RumEvent fromSpanEnvelopeItem(Span span, long rootFallbackTime) {
    RumEvent event = new RumEvent();
    event.setSpan(span);
    event.setResource(span.getResource());
    event.setStatus(span.getStatus());
    long startTime = spanEventTime(span, rootFallbackTime);
    event.setEventTime(startTime);
    event.setEndTime(spanEndTime(span, startTime));
    String sessionId = SpanAttributeUtils.getAttributeString(span.getAttributes(), SESSION_ID);
    String spanType = SpanAttributeUtils.getAttributeString(span.getAttributes(), SPAN_TYPE);
    event.setSpanType(spanType);
    event.setSessionId(sessionId);
    return event;
  }

  /**
   * 兼容两类历史 flat event:
   *
   * <ul>
   *   <li>已归一化的 RumEvent：{@code span/sessionId/eventTime} 等字段在 root;
   *   <li>展平的 span：{@code attributes/span_name/start_time} 等 span 字段在 root，并可使用
   *       {@code session_id/event_time/span_type} 别名。
   * </ul>
   */
  @Nullable
  private RumEvent fromFlatEvent(ObjectNode root) throws IOException {
    JsonNode spanNode = object(root, "span");
    boolean normalizedEvent = root.has("span");
    boolean flattenedSpan =
        object(root, "attributes") != null
            || find(root, "span_name", "spanName", "start_time", "startTime") != null
            || find(root, "session_id", "sessionId", "event_time", "eventTime") != null;
    if (!normalizedEvent && !flattenedSpan) {
      return null;
    }

    RumEvent event = normalizedEvent ? MAPPER.treeToValue(root, RumEvent.class) : new RumEvent();
    Span span =
        spanNode != null
            ? MAPPER.treeToValue(spanNode, Span.class)
            : MAPPER.treeToValue(root, Span.class);
    event.setSpan(span);
    if (event.getResource() == null) {
      event.setResource(span.getResource());
    }
    if (event.getStatus() == null) {
      event.setStatus(span.getStatus());
    }
    if (event.getEventTime() <= 0L) {
      event.setEventTime(
          timestamp(
              root,
              "event_time",
              "eventTime",
              "start_time",
              "startTime",
              "timestamp",
              "time",
              "ts"));
    } else {
      // Jackson 直接映射的 eventTime 未经单位归一化，这里统一到微秒。
      event.setEventTime(normalizeEpochMicros(event.getEventTime()));
    }
    if (event.getEndTime() > 0L) {
      event.setEndTime(normalizeEpochMicros(event.getEndTime()));
    } else {
      Long spanEnd = span.getEndTime();
      event.setEndTime(
          spanEnd != null && spanEnd > 0L
              ? normalizeEpochMicros(spanEnd)
              : event.getEventTime());
    }
    if (event.getSessionId() == null || event.getSessionId().trim().isEmpty()) {
      event.setSessionId(
          coalesce(
              text(root, "session_id", "sessionId"),
              SpanAttributeUtils.getAttributeString(span.getAttributes(), SESSION_ID)));
    }
    if (event.getSpanType() == null || event.getSpanType().trim().isEmpty()) {
      event.setSpanType(
          coalesce(
              text(root, "span_type", "spanType"),
              SpanAttributeUtils.getAttributeString(span.getAttributes(), SPAN_TYPE)));
    }
    return event;
  }

  /** 记录 record 级坏数据；日志仅输出长度和错误摘要，不泄露原始 payload。 */
  private void markMalformedRecord(int messageBytes, String reason, Throwable cause) {
    if (malformedRecordCounter != null) {
      malformedRecordCounter.inc();
    }
    long count = ++malformedRecordCount;
    if (shouldLogMalformed(count)) {
      LOG.warn(
          "skip malformed rum record: reason={}, messageBytes={}, error={}, occurrence={}",
          reason,
          messageBytes,
          errorSummary(cause),
          count);
      logMalformedStackAtDebug("rum record", cause);
    }
  }

  /** 记录 item 级坏数据，保留同一 envelope 内其它合法 item。 */
  private void markMalformedItem(int itemIndex, String reason, Throwable cause) {
    if (malformedItemCounter != null) {
      malformedItemCounter.inc();
    }
    long count = ++malformedItemCount;
    if (shouldLogMalformed(count)) {
      LOG.warn(
          "skip malformed rum envelope item: itemIndex={}, reason={}, error={}, occurrence={}",
          itemIndex,
          reason,
          errorSummary(cause),
          count);
      logMalformedStackAtDebug("rum envelope item", cause);
    }
  }

  /** 前 10 次全部打印，之后每 1000 次打印一条摘要。 */
  private static boolean shouldLogMalformed(long count) {
    return count <= 10L || count % 1_000L == 0L;
  }

  private static String errorSummary(@Nullable Throwable cause) {
    if (cause == null) {
      return "none";
    }
    String message = cause.getMessage();
    String summary =
        cause.getClass().getSimpleName()
            + (message == null || message.trim().isEmpty() ? "" : ": " + message.trim());
    return summary.length() <= 256 ? summary : summary.substring(0, 256);
  }

  private static void logMalformedStackAtDebug(String scope, @Nullable Throwable cause) {
    if (cause != null && LOG.isDebugEnabled()) {
      LOG.debug("malformed {} stack trace", scope, cause);
    }
  }

  /**
   * 事件时间:优先取 span 自身的 start_time(归一化后),缺省回退到 root 级时间字段 (从 streaming 扫描时已预先解析的
   * {@code rootFallbackTime} 传入,避免重新解析 root)。
   */
  private static long spanEventTime(Span span, long rootFallbackTime) {
    Long start = span.getStartTime();
    if (start != null && start > 0L) {
      return normalizeEpochMicros(start);
    }
    return rootFallbackTime;
  }

  /** 结束时间优先使用 span.end_time，缺失时回退到已归一化的开始时间。 */
  private static long spanEndTime(Span span, long normalizedStartTime) {
    Long end = span.getEndTime();
    if (end != null && end > 0L) {
      return normalizeEpochMicros(end);
    }
    return normalizedStartTime;
  }

  /**
   * 从当前 value token 直接读出微秒 epoch，避免构造中间 JsonNode。数值走
   * {@link #normalizeEpochMicros}，字符串走 {@link #timestampFromText}，与 {@link
   * #timestamp(JsonNode, String...)} 语义一致。复合 token 内部 skipChildren 返回 0L。
   */
  private static long readScalarAsEpochMicros(JsonParser p, JsonToken next) throws IOException {
    if (next == null || next == JsonToken.VALUE_NULL) {
      return 0L;
    }
    if (next == JsonToken.VALUE_NUMBER_INT || next == JsonToken.VALUE_NUMBER_FLOAT) {
      return normalizeEpochMicros(p.getValueAsLong());
    }
    if (next == JsonToken.VALUE_STRING) {
      return timestampFromText(p.getValueAsString());
    }
    p.skipChildren();
    return 0L;
  }

  /** 从当前 value token 直接读出字符串字段；非叶子 token 内部 skipChildren，返回 null。 */
  @Nullable
  private static String readScalarAsString(JsonParser p, JsonToken next) throws IOException {
    if (next == null || next == JsonToken.VALUE_NULL) {
      return null;
    }
    if (next == JsonToken.VALUE_STRING) {
      String s = p.getValueAsString();
      return s == null || s.trim().isEmpty() ? null : s;
    }
    p.skipChildren();
    return null;
  }

  /** 从当前 value token 直接读出整数字段；非叶子 token 内部 skipChildren，返回 null。 */
  @Nullable
  private static Integer readScalarAsInteger(JsonParser p, JsonToken next) throws IOException {
    if (next == null || next == JsonToken.VALUE_NULL) {
      return null;
    }
    if (next == JsonToken.VALUE_NUMBER_INT || next == JsonToken.VALUE_NUMBER_FLOAT) {
      return (int) p.getValueAsLong();
    }
    if (next == JsonToken.VALUE_STRING) {
      String s = p.getValueAsString();
      if (s == null || s.trim().isEmpty()) {
        return null;
      }
      try {
        return Integer.parseInt(s.trim());
      } catch (NumberFormatException ignored) {
        return null;
      }
    }
    p.skipChildren();
    return null;
  }

  /** 取子对象;不存在或不是 object 时返回 null。 */
  @Nullable
  private static JsonNode object(@Nullable JsonNode node, String name) {
    if (node == null) {
      return null;
    }
    JsonNode value = node.get(name);
    return value != null && value.isObject() ? value : null;
  }

  /** 按候选字段名依次查找,返回第一个非空、非空白文本值;都缺失返回 null。无 names 时把 node 当作叶子值。 */
  @Nullable
  private static String text(@Nullable JsonNode node, String... names) {
    JsonNode value = names.length == 0 ? node : find(node, names);
    if (value == null || value.isNull()) {
      return null;
    }
    String text = value.asText();
    return text == null || text.trim().isEmpty() ? null : text;
  }

  /** 取 long 值,容忍数值型与字符串型;非数字字符串返回 null。无 names 时把 node 当作叶子值。 */
  @Nullable
  private static Long longValue(@Nullable JsonNode node, String... names) {
    JsonNode value = names.length == 0 ? node : find(node, names);
    if (value == null || value.isNull()) {
      return null;
    }
    if (value.isNumber()) {
      return value.asLong();
    }
    String text = value.asText();
    if (text == null || text.trim().isEmpty()) {
      return null;
    }
    try {
      return Long.parseLong(text.trim());
    } catch (NumberFormatException ignored) {
      return null;
    }
  }

  @Nullable
  private static Integer integerValue(@Nullable JsonNode node, String... names) {
    Long value = longValue(node, names);
    return value == null ? null : value.intValue();
  }

  /**
   * 解析时间字段为微秒 epoch。解析顺序:
   *
   * <ol>
   *   <li>数值 -> 经 {@link #normalizeEpochMicros} 归一化(兼容秒/毫秒/微秒/纳秒);
   *   <li>纯数字字符串 -> 同上;
   *   <li>ISO-8601(如 {@code 2026-08-04T12:46:35Z});
   *   <li>{@code yyyy-MM-dd HH:mm:ss}(按 UTC 解析);
   *   <li>全部失败返回 0。
   * </ol>
   *
   * <p>无 {@code names} 时把 {@code node} 当作叶子值直接归一化(用于 envelope 扫描时已捕获的 root 字段)。
   */
  private static long timestamp(@Nullable JsonNode node, String... names) {
    JsonNode value = names.length == 0 ? node : find(node, names);
    if (value == null || value.isNull()) {
      return 0L;
    }
    if (value.isNumber()) {
      return normalizeEpochMicros(value.asLong());
    }
    return timestampFromText(value.asText());
  }

  /**
   * 把已经读取出来的字符串或纯数字文本解析为微秒 epoch;文本逻辑与 {@link #timestamp} 的非数值分支共用。
   * streaming 扫描 root 时间字段时使用。
   */
  private static long timestampFromText(@Nullable String text) {
    if (text == null || text.trim().isEmpty()) {
      return 0L;
    }
    String trimmed = text.trim();
    try {
      return normalizeEpochMicros(Long.parseLong(trimmed));
    } catch (NumberFormatException ignored) {
      try {
        return toEpochMicros(Instant.parse(trimmed));
      } catch (DateTimeParseException ignoredAgain) {
        try {
          return toEpochMicros(
              LocalDateTime.parse(trimmed, SPACE_DATE_TIME).toInstant(ZoneOffset.UTC));
        } catch (DateTimeParseException ignoredThird) {
          return 0L;
        }
      }
    }
  }

  /** {@link Instant} 转微秒 epoch（预计算链路统一单位）。 */
  private static long toEpochMicros(Instant instant) {
    return Math.multiplyExact(instant.getEpochSecond(), TimeUtils.SECOND_TO_US)
        + instant.getNano() / TimeUtils.NANO_TO_US;
  }

  /**
   * 把任意精度的 epoch 时间归一化为微秒（预计算链路统一单位）。阈值依据数量级判断单位:
   *
   * <ul>
   *   <li>&gt; 1e17:纳秒,除以 1e3;
   *   <li>&gt; 1e14:已是微秒,原样返回;
   *   <li>&lt; 1e11 且 &gt; 0:秒,乘以 1e6;
   *   <li>其余(1e11 ~ 1e14):毫秒,乘以 1e3。
   * </ul>
   *
   * 注意阈值是经验值,2001-2286 年间的毫秒时间戳恰落在 1e11~1e14 区间。
   */
  private static long normalizeEpochMicros(long timestamp) {
    if (timestamp > 100_000_000_000_000_000L) {
      return timestamp / TimeUtils.NANO_TO_US;
    }
    if (timestamp > 100_000_000_000_000L) {
      return timestamp;
    }
    if (timestamp > 0L && timestamp < 100_000_000_000L) {
      return timestamp * TimeUtils.SECOND_TO_US;
    }
    if (timestamp > 0L) {
      return timestamp * TimeUtils.MS_TO_US;
    }
    return timestamp;
  }

  /** 在 node 上按候选字段名依次查找,返回第一个非 missing 节点;全缺失返回 null。 */
  @Nullable
  private static JsonNode find(@Nullable JsonNode node, String... names) {
    if (node == null) {
      return null;
    }
    for (String name : names) {
      JsonNode value = node.get(name);
      if (value != null && !value.isMissingNode()) {
        return value;
      }
    }
    return null;
  }

  /** 返回第一个非空、非空白字符串;全为空返回 null。 */
  @Nullable
  private static String coalesce(String... values) {
    for (String value : values) {
      if (value != null && !value.trim().isEmpty()) {
        return value;
      }
    }
    return null;
  }
}
