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
import static org.assertj.core.api.Assertions.catchThrowableOfType;

import java.nio.file.Files;
import java.nio.file.Path;
import org.apache.flink.util.ParameterTool;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * 验证 {@link RumPrecomputeJob#loadParameters(String[])} 的 JSON 配置加载与命令行合并行为。
 *
 * <p>{@link RumPrecomputeJob#main(String[])} 本身依赖 Flink 执行环境与 JobClient,这些场景在 {@link
 * RumJobTopologyTest} 中已通过 {@link RumJobConfig#from(ParameterTool)} 间接覆盖;这里只断言「如何把 JSON
 * 文件 + 命令行参数合并成一份 {@link ParameterTool}」。
 */
class RumPrecomputeJobConfigTest {

  @TempDir Path tmp;

  @Test
  void loadsConfigFromJsonFile() throws Exception {
    Path json =
        writeJson(
            "{\n"
                + "  \"kafka.bootstrap.servers\": \"localhost:9092\",\n"
                + "  \"kafka.topic\": \"rum-events\",\n"
                + "  \"sink.type\": \"es\"\n"
                + "}\n");

    ParameterTool params =
        RumPrecomputeJob.loadParameters(new String[] {"--config", json.toString()});

    assertThat(params.get("kafka.bootstrap.servers")).isEqualTo("localhost:9092");
    assertThat(params.get("kafka.topic")).isEqualTo("rum-events");
    assertThat(params.get("sink.type")).isEqualTo("es");
    assertThat(
        params.has("config"))
        .withFailMessage("the --config flag itself must not leak into business parameters")
        .isFalse();
  }

  @Test
  void commandLineOverridesJsonFile() throws Exception {
    Path json =
        writeJson(
            "{\n"
                + "  \"kafka.bootstrap.servers\": \"from-json:9092\",\n"
                + "  \"kafka.topic\": \"from-json\",\n"
                + "  \"sink.type\": \"es\"\n"
                + "}\n");

    ParameterTool params =
        RumPrecomputeJob.loadParameters(
            new String[] {
              "--config", json.toString(),
              "--kafka.bootstrap.servers", "from-cli:9092",
              "--kafka.topic", "from-cli"
            });

    assertThat(params.get("kafka.bootstrap.servers")).isEqualTo("from-cli:9092");
    assertThat(params.get("kafka.topic")).isEqualTo("from-cli");
    assertThat(params.get("sink.type")).isEqualTo("es");
  }

  @Test
  void worksWithoutConfigFlag() {
    ParameterTool params =
        RumPrecomputeJob.loadParameters(new String[] {"--kafka.topic", "rum-events"});

    assertThat(params.get("kafka.topic")).isEqualTo("rum-events");
    assertThat(params.has("config")).isFalse();
  }

  @Test
  void rejectsMissingConfigFile() {
    Path missing = tmp.resolve("does-not-exist.json");
    IllegalArgumentException ex =
        catchThrowableOfType(
            () ->
                RumPrecomputeJob.loadParameters(
                    new String[] {"--config", missing.toString()}),
            IllegalArgumentException.class);
    assertThat(
        ex.getMessage().contains("JSON config file not found"))
        .withFailMessage("message should clearly identify the failure mode: " + ex.getMessage())
        .isTrue();
  }

  @Test
  void rejectsTopLevelArray() throws Exception {
    Path json = writeJson("[\"kafka.bootstrap.servers\", \"localhost:9092\"]\n");
    IllegalArgumentException ex =
        catchThrowableOfType(
            () ->
                RumPrecomputeJob.loadParameters(
                    new String[] {"--config", json.toString()}),
            IllegalArgumentException.class);
    assertThat(ex.getMessage().contains("must be an object")).isTrue();
  }

  @Test
  void rejectsNestedObjectValues() throws Exception {
    Path json =
        writeJson(
            "{\n"
                + "  \"kafka.topic\": \"rum-events\",\n"
                + "  \"kafka\": {\"bootstrap.servers\": \"localhost:9092\"}\n"
                + "}\n");
    IllegalArgumentException ex =
        catchThrowableOfType(
            () ->
                RumPrecomputeJob.loadParameters(
                    new String[] {"--config", json.toString()}),
            IllegalArgumentException.class);
    assertThat(
        ex.getMessage().contains("must be a string"))
        .withFailMessage("message should call out non-string values: " + ex.getMessage())
        .isTrue();
  }

  @Test
  void rejectsNonStringScalarValues() throws Exception {
    Path json =
        writeJson(
            "{\n"
                + "  \"kafka.topic\": \"rum-events\",\n"
                + "  \"parallelism\": 4\n"
                + "}\n");
    IllegalArgumentException ex =
        catchThrowableOfType(
            () ->
                RumPrecomputeJob.loadParameters(
                    new String[] {"--config", json.toString()}),
            IllegalArgumentException.class);
    assertThat(ex.getMessage().contains("parallelism")).isTrue();
  }

  private Path writeJson(String body) throws Exception {
    Path file = tmp.resolve("config-" + System.nanoTime() + ".json");
    Files.writeString(file, body);
    return file;
  }
}
