// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import javax.annotation.Nullable;
import lombok.Data;

/**
 * RUM(Resource Usage Monitoring)上报数据中的 resource 段,对应 OpenTelemetry 资源属性。
 *
 * <p>字段命名遵循 <a
 * href="https://opentelemetry.io/docs/specs/semconv/resource/">OTel Semantic Conventions</a>,
 * 通过 {@link JsonProperty} 绑定 dot 形式的属性 key,反序列化时由 {@code
 * RumEventDeserializationSchema} 解析。Java 字段沿用 JavaBean 驼峰命名;{@link JsonProperty} 与 ES 字段名均保持 OTel 原始形式。
 */
@Data
public class Resource {
  // ---- 应用 / 环境 ----

  /** 部署环境名称,如 {@code production} / {@code staging}。 */
  @Nullable
  @JsonProperty("deployment.environment.name")
  private String deploymentEnvironmentName;

  /** 服务名,标识产生 RUM 数据的前端应用或微服务。 */
  @Nullable
  @JsonProperty("service.name")
  private String serviceName;

  /** 服务版本号,通常与前端构建产物版本号一致。 */
  @Nullable
  @JsonProperty("service.version")
  private String serviceVersion;

  /** Telemetry SDK 名称,如 {@code datadog-rum}。 */
  @Nullable
  @JsonProperty("telemetry.sdk.name")
  private String telemetrySdkName;

  /** Telemetry SDK 版本号。 */
  @Nullable
  @JsonProperty("telemetry.sdk.version")
  private String telemetrySdkVersion;

  /** 生成该数据的 Telemetry SDK 所用语言,如 {@code webjs}。 */
  @Nullable
  @JsonProperty("telemetry.sdk.language")
  private String telemetrySdkLanguage;

  // ---- 终端(本次阶段未纳入改造,字段保持驼峰) ----

  /** 设备类型,如 {@code mobile} / {@code tablet} / {@code desktop}。 */
  @Nullable
  @JsonProperty("device.type")
  private String deviceType;

  /** 会话采样率(0-100,百分比),由 SDK 端按配置抽样决定是否上报。 */
  @Nullable
  @JsonProperty("session.sample_rate")
  private Integer sessionSampleRate;

  /** 浏览器/客户端名称,如 {@code Chrome} / {@code Safari}。 */
  @Nullable
  @JsonProperty("user_agent.name")
  private String userAgentName;

  /** 操作系统名称,如 {@code Mac OS} / {@code Windows} / {@code iOS}。 */
  @Nullable
  @JsonProperty("user_agent.os.name")
  private String userAgentOsName;

  /** 浏览器/客户端版本号。 */
  @Nullable
  @JsonProperty("user_agent.version")
  private String userAgentVersion;
}
