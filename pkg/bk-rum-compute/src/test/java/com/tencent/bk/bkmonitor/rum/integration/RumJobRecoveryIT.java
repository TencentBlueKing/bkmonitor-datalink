// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.integration;

import static org.apache.flink.runtime.testutils.CommonTestUtils.waitForAllTaskRunning;
import static org.apache.flink.runtime.testutils.CommonTestUtils.waitUntilCondition;
import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.tencent.bk.bkmonitor.rum.RumJobConfig;
import com.tencent.bk.bkmonitor.rum.RumJobTopology;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.apache.flink.api.common.JobID;
import org.apache.flink.configuration.CheckpointingOptions;
import org.apache.flink.configuration.Configuration;
import org.apache.flink.configuration.RestartStrategyOptions;
import org.apache.flink.core.execution.CheckpointType;
import org.apache.flink.runtime.checkpoint.CompletedCheckpointStats;
import org.apache.flink.runtime.checkpoint.RestoredCheckpointStats;
import org.apache.flink.runtime.minicluster.MiniCluster;
import org.apache.flink.runtime.minicluster.MiniClusterConfiguration;
import org.apache.flink.streaming.api.environment.StreamExecutionEnvironment;
import org.apache.flink.util.ParameterTool;
import org.apache.http.HttpHost;
import org.apache.kafka.clients.admin.Admin;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.serialization.StringDeserializer;
import org.apache.kafka.common.serialization.StringSerializer;
import org.elasticsearch.client.Request;
import org.elasticsearch.client.RestClient;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.containers.wait.strategy.Wait;
import org.testcontainers.kafka.KafkaContainer;

/** Exercises the production topology against isolated services; run with the integration-tests profile. */
class RumJobRecoveryIT {
    private static final Logger LOG = LoggerFactory.getLogger(RumJobRecoveryIT.class);
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final String INDEX_PATTERN = "write_*_rum_global_*_precalculate_auto_*";
    private static final long FIRST_EVENT_US = Instant.parse("2020-01-02T00:00:01Z").toEpochMilli() * 1_000;
    private static final long LATE_EVENT_US = FIRST_EVENT_US - Duration.ofDays(1).toMillis() * 1_000;
    private static final List<Integer> TENANTS = List.of(42, 43);

    @TempDir
    Path checkpoints;

    @Test
    void taskManagerFailureReplaysKafkaAndPreservesClosedWindowsAndAudit() throws Exception {
        // A dedicated ES instance makes every document observable, including accidental reopened windows.
        try (KafkaContainer kafka = new KafkaContainer("apache/kafka:4.3.0")
                .withEnv("KAFKA_HEAP_OPTS", "-Xms256m -Xmx256m");
                GenericContainer<?> elasticsearch = new GenericContainer<>(
                        "docker.elastic.co/elasticsearch/elasticsearch:7.17.28")
                        .withEnv("discovery.type", "single-node")
                        .withEnv("xpack.security.enabled", "false")
                        .withEnv("node.processors", "2")
                        .withEnv("ES_JAVA_OPTS", "-Xms512m -Xmx512m")
                        .withExposedPorts(9200)
                        .waitingFor(Wait.forHttp("/_cluster/health?wait_for_status=yellow").forPort(9200))) {
            kafka.start();
            elasticsearch.start();
            String esAddress = "http://" + elasticsearch.getHost() + ":" + elasticsearch.getMappedPort(9200);
            try (RestClient es = RestClient.builder(HttpHost.create(esAddress)).build()) {
                installTemplates(es);
                Map<String, JsonNode> baseline = runScenario(kafka.getBootstrapServers(), esAddress, es, false);
                es.performRequest(new Request("DELETE", "/" + INDEX_PATTERN));

                Map<String, JsonNode> recovered = runScenario(kafka.getBootstrapServers(), esAddress, es, true);

                assertThat(businessFields(recovered)).isEqualTo(businessFields(baseline));
            }
        }
    }

    private Map<String, JsonNode> runScenario(
            String broker, String esAddress, RestClient es, boolean failTaskManager) throws Exception {
        String topic = failTaskManager ? "rum-recovery" : "rum-baseline";
        String auditTopic = topic + "-late";
        try (Admin admin = Admin.create(Map.of("bootstrap.servers", broker))) {
            admin.createTopics(List.of(
                    new NewTopic(topic, 2, (short) 1), new NewTopic(auditTopic, 1, (short) 1))).all().get();
        }
        Configuration configuration = new Configuration();
        configuration.set(CheckpointingOptions.CHECKPOINT_STORAGE, "filesystem");
        configuration.set(CheckpointingOptions.CHECKPOINTS_DIRECTORY, checkpoints.resolve(topic).toUri().toString());
        // Only explicit checkpoints may advance the recovery point in this scenario.
        configuration.set(CheckpointingOptions.CHECKPOINTING_INTERVAL, Duration.ofDays(1));
        configuration.set(RestartStrategyOptions.RESTART_STRATEGY, "fixed-delay");
        configuration.set(RestartStrategyOptions.RESTART_STRATEGY_FIXED_DELAY_ATTEMPTS, 1);
        configuration.set(RestartStrategyOptions.RESTART_STRATEGY_FIXED_DELAY_DELAY, Duration.ofMillis(100));
        MiniClusterConfiguration clusterConfiguration = new MiniClusterConfiguration.Builder()
                .setConfiguration(configuration).setNumTaskManagers(2).setNumSlotsPerTaskManager(1).build();

        try (MiniCluster cluster = new MiniCluster(clusterConfiguration);
                KafkaProducer<String, String> producer = new KafkaProducer<>(
                        Map.of("bootstrap.servers", broker, "acks", "all"),
                        new StringSerializer(), new StringSerializer());
                KafkaConsumer<String, String> audit = new KafkaConsumer<>(
                        Map.of("bootstrap.servers", broker, "group.id", auditTopic,
                                "auto.offset.reset", "earliest", "enable.auto.commit", "false"),
                        new StringDeserializer(), new StringDeserializer())) {
            cluster.start();
            StreamExecutionEnvironment env = StreamExecutionEnvironment.getExecutionEnvironment(configuration);
            env.setParallelism(2);
            RumJobTopology.build(env, jobConfig(broker, topic, auditTopic, esAddress));
            JobID jobId = cluster.submitJob(env.getStreamGraph()).get().getJobID();
            try {
                waitForAllTaskRunning(cluster, jobId, false);
                audit.assign(List.of(new TopicPartition(auditTopic, 0)));
                publish(producer, topic, 1);
                Map<String, JsonNode> beforeCheckpoint = awaitDocuments(es, cluster, jobId, 1);
                long checkpointId = completedCheckpoint(cluster, jobId);
                LOG.info("Recovery IT: scenario={}, checkpoint={} completed", topic, checkpointId);

                // These writes reach ES and the audit topic, but their Kafka offsets are outside the checkpoint.
                publish(producer, topic, 1);
                publish(producer, topic, 2);
                awaitDocuments(es, cluster, jobId, 2);
                Map<String, Integer> auditCounts = new HashMap<>();
                awaitAudit(audit, auditCounts, cluster, jobId, "event-2", 1);

                if (failTaskManager) {
                    cluster.terminateTaskManager(0).get();
                    cluster.startTaskManager();
                    waitUntilCondition(() -> {
                        assertJobAlive(cluster, jobId);
                        RestoredCheckpointStats restored = cluster.getExecutionGraph(jobId).get()
                                .getCheckpointStatsSnapshot().getLatestRestoredCheckpoint();
                        return restored != null && restored.getCheckpointId() == checkpointId;
                    });
                    waitForAllTaskRunning(cluster, jobId, false);
                    // Observing the same audit identities again proves source replay actually occurred.
                    awaitAudit(audit, auditCounts, cluster, jobId, "event-2", 2);
                    LOG.info("Recovery IT: restored checkpoint={} and observed Kafka replay", checkpointId);
                }

                publish(producer, topic, 2);
                publish(producer, topic, 3);
                completedCheckpoint(cluster, jobId);
                Map<String, JsonNode> result = awaitDocuments(es, cluster, jobId, 3);
                awaitAudit(audit, auditCounts, cluster, jobId, "event-3", 1);
                drainAudit(audit, auditCounts);

                assertThat(auditCounts).containsOnlyKeys("42:event-2", "43:event-2", "42:event-3", "43:event-3");
                assertSameWindows(beforeCheckpoint, result);
                LOG.info("Recovery IT: scenario={} verified 4 final documents and complete late audit", topic);
                return result;
            } finally {
                cluster.cancelJob(jobId).get();
            }
        }
    }

    private static RumJobConfig jobConfig(String broker, String topic, String auditTopic, String esAddress) {
        Map<String, String> values = new HashMap<>();
        values.put("kafka.bootstrap.servers", broker);
        values.put("kafka.topic", topic);
        values.put("kafka.group.id", topic);
        values.put("kafka.startup.mode", "earliest");
        values.put("kafka.fetch.min.bytes", "1");
        values.put("kafka.fetch.max.wait.ms", "20");
        values.put("sink.type", "elasticsearch");
        values.put("sink.es.hosts", esAddress);
        values.put("sink.es.bulk.max.actions", "1");
        values.put("sink.es.bulk.flush.interval.ms", "20");
        values.put("precalculate.dispersed-count", "1");
        values.put("late.sink.type", "kafka");
        values.put("late.kafka.topic", auditTopic);
        return RumJobConfig.from(ParameterTool.fromMap(values));
    }

    private static void publish(KafkaProducer<String, String> producer, String topic, int event) throws Exception {
        long timestamp = event == 2 ? LATE_EVENT_US : FIRST_EVENT_US + (event - 1) * 1_000L;
        for (int tenant : TENANTS) {
            ObjectNode envelope = JSON.createObjectNode().put("bk_biz_id", tenant).put("app_name", "recovery-it");
            ObjectNode span = envelope.putArray("items").addObject()
                    .put("trace_id", "trace-" + event).put("span_id", "event-" + event)
                    .put("span_name", "click").put("start_time", timestamp).put("end_time", timestamp + 100);
            span.putArray("links").addObject().put("trace_id", "linked-trace-" + event)
                    .put("span_id", "linked-span-" + event);
            ObjectNode attributes = span.putObject("attributes")
                    .put("session.id", "shared-session").put("view.id", "shared-view").put("span_type", "click")
                    .put("view.started_at", FIRST_EVENT_US / 1_000);
            if (event == 1) {
                attributes.put("session.phase", "end").put("view.phase", "end");
            }
            producer.send(new ProducerRecord<>(topic, tenant - 42, "shared-session", envelope.toString())).get();
        }
    }

    private static long completedCheckpoint(MiniCluster cluster, JobID jobId) throws Exception {
        long checkpointId = cluster.triggerCheckpoint(jobId, CheckpointType.FULL).get();
        waitUntilCondition(() -> {
            assertJobAlive(cluster, jobId);
            CompletedCheckpointStats completed = cluster.getExecutionGraph(jobId).get()
                    .getCheckpointStatsSnapshot().getHistory().getLatestCompletedCheckpoint();
            return completed != null && completed.getCheckpointId() == checkpointId;
        });
        return checkpointId;
    }

    private static Map<String, JsonNode> awaitDocuments(
            RestClient es, MiniCluster cluster, JobID jobId, int actions) throws Exception {
        Map<String, JsonNode> documents = new HashMap<>();
        waitUntilCondition(() -> {
            assertJobAlive(cluster, jobId);
            documents.clear();
            Request refresh = new Request("POST", "/" + INDEX_PATTERN + "/_refresh");
            refresh.addParameter("allow_no_indices", "true");
            es.performRequest(refresh);
            Request search = new Request("GET", "/" + INDEX_PATTERN + "/_search");
            search.addParameter("size", "100");
            JsonNode hits = JSON.readTree(es.performRequest(search).getEntity().getContent()).path("hits");
            assertThat(hits.path("total").path("value").asInt()).isLessThanOrEqualTo(4);
            for (JsonNode hit : hits.path("hits")) {
                String key = hit.path("_index").asText() + ":" + hit.path("_source").path("bk_biz_id").asText();
                assertThat(documents.put(key, hit)).as("Only one window per tenant and family: %s", key).isNull();
                assertThat(hit.path("_source").path("action_count").asInt()).isLessThanOrEqualTo(actions);
            }
            return documents.size() == 4 && documents.values().stream()
                    .allMatch(hit -> hit.path("_source").path("action_count").asInt() == actions);
        });
        return documents;
    }

    private static void assertSameWindows(Map<String, JsonNode> initial, Map<String, JsonNode> result) {
        assertThat(result.keySet()).containsExactlyInAnyOrderElementsOf(initial.keySet());
        for (Map.Entry<String, JsonNode> entry : result.entrySet()) {
            JsonNode hit = entry.getValue();
            JsonNode source = hit.path("_source");
            JsonNode original = initial.get(entry.getKey());
            assertThat(hit.path("_id")).isEqualTo(original.path("_id"));
            assertThat(source.path("window_id").asText()).isNotBlank();
            assertThat(source.path("window_id")).isEqualTo(original.path("_source").path("window_id"));
            assertThat(hit.path("_id").asText()).endsWith(":" + source.path("window_id").asText());
            assertThat(hit.path("_index").asText()).startsWith("write_20200102_");
            assertThat(source.path("date").asText()).isEqualTo("2020-01-02");
            assertThat(source.path("closed").asBoolean()).isTrue();
            assertThat(source.path("is_active").asBoolean()).isFalse();
            assertThat(source.path("close_reason").asText()).isEqualTo("normal");
            assertThat(source.path("close_time").asLong()).isPositive();
            assertThat(source.path("close_time")).isEqualTo(original.path("_source").path("close_time"));
            assertThat(source.path("action_count").asInt()).isEqualTo(3);
            assertThat(source.path("trace_count").asInt()).isEqualTo(3);
            if (source.has("attributes.view.id")) {
                assertThat(source.path("min_start_time").asLong()).isEqualTo(FIRST_EVENT_US);
            } else {
                assertThat(source.path("min_start_time").asLong()).isEqualTo(LATE_EVENT_US);
                assertThat(source.path("view_count").asInt()).isEqualTo(1);
            }
            assertThat(source.path("max_end_time").asLong()).isEqualTo(FIRST_EVENT_US + 2_100);
        }
    }

    private static void awaitAudit(
            KafkaConsumer<String, String> audit, Map<String, Integer> counts,
            MiniCluster cluster, JobID jobId, String spanId, int minimumCopies) throws Exception {
        waitUntilCondition(() -> {
            assertJobAlive(cluster, jobId);
            readAudit(audit, counts);
            return TENANTS.stream()
                    .allMatch(tenant -> counts.getOrDefault(tenant + ":" + spanId, 0) >= minimumCopies);
        });
    }

    private static void readAudit(KafkaConsumer<String, String> audit, Map<String, Integer> counts) throws Exception {
        for (ConsumerRecord<String, String> record : audit.poll(Duration.ofMillis(100))) {
            JsonNode span = JSON.readTree(record.value()).path("span");
            String identity = span.path("bk_biz_id").asText() + ":" + span.path("span_id").asText();
            counts.merge(identity, 1, Integer::sum);
        }
    }

    private static void drainAudit(KafkaConsumer<String, String> audit, Map<String, Integer> counts) throws Exception {
        Map<TopicPartition, Long> endOffsets = audit.endOffsets(audit.assignment());
        for (Map.Entry<TopicPartition, Long> end : endOffsets.entrySet()) {
            while (audit.position(end.getKey()) < end.getValue()) {
                readAudit(audit, counts);
            }
        }
    }

    private static Map<String, JsonNode> businessFields(Map<String, JsonNode> documents) {
        Map<String, JsonNode> result = new HashMap<>();
        for (Map.Entry<String, JsonNode> entry : documents.entrySet()) {
            ObjectNode source = entry.getValue().path("_source").deepCopy();
            // Independent runs allocate different window IDs and processing timestamps.
            source.remove(List.of("window_id", "time", "updated_at_ts", "close_time"));
            result.put(entry.getKey(), source);
        }
        return result;
    }

    private static void assertJobAlive(MiniCluster cluster, JobID jobId) throws Exception {
        if (cluster.getJobStatus(jobId).get().isGloballyTerminalState()) {
            throw new AssertionError("Job terminated while waiting for recovery: "
                    + cluster.getArchivedExecutionGraph(jobId).get().getFailureInfo());
        }
    }

    private static void installTemplates(RestClient es) throws Exception {
        for (String family : List.of("session", "view")) {
            Request request = new Request("PUT", "/_index_template/rum-" + family + "-recovery-it");
            request.setJsonEntity(Files.readString(
                    Path.of("docs", "elasticsearch", "rum-" + family + "-precalculate-auto-template.json")));
            es.performRequest(request);
        }
    }
}
