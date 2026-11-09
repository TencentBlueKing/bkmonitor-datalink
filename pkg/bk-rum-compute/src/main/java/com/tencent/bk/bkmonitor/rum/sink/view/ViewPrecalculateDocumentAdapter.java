// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink.view;

import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateFamily;
import com.tencent.bk.bkmonitor.rum.sink.PrecalculateDocumentAdapter;
import com.tencent.bk.bkmonitor.rum.utils.RumEntityKey;

import java.util.LinkedHashMap;
import java.util.Map;

/** View 聚合文档到预计算 ES 字段的适配器。 */
public final class ViewPrecalculateDocumentAdapter
    implements PrecalculateDocumentAdapter<ViewEventDocument> {

  private static final long serialVersionUID = 1L;
  static final ViewPrecalculateDocumentAdapter INSTANCE = new ViewPrecalculateDocumentAdapter();

  private ViewPrecalculateDocumentAdapter() {}

  @Override
  public PrecalculateFamily family() {
    return PrecalculateFamily.VIEW;
  }

  @Override
  public boolean hasRequiredRoutingFields(ViewEventDocument document) {
    return document.getBkBizId() != null
        && document.getAppName() != null
        && !document.getAppName().isBlank()
        && document.getViewId() != null
        && !document.getViewId().isBlank()
        && document.getWindowId() != null
        && !document.getWindowId().isBlank()
        && document.getDate() != null
        && !document.getDate().isBlank();
  }

  @Override
  public long bkBizId(ViewEventDocument document) {
    return document.getBkBizId();
  }

  @Override
  public String appName(ViewEventDocument document) {
    return document.getAppName();
  }

  @Override
  public String documentId(ViewEventDocument document) {
    return RumEntityKey.documentId(
        document.getBkBizId(), document.getAppName(), document.getViewId(), document.getWindowId());
  }

  @Override
  public String indexDate(ViewEventDocument document) {
    return document.getDate();
  }

  @Override
  public Map<String, Object> toDocumentFields(ViewEventDocument s) {
    return toDocument(s);
  }

  /**
   * 把 ViewEventDocument 映射为 ES 文档字段，与 {@code rum-view-precalculate-auto} 模板对齐。
   *
   * <p>通用的预计算处理时刻 {@code time} 由 emitter 统一补充。
   */
  static Map<String, Object> toDocument(ViewEventDocument s) {
    Map<String, Object> document = new LinkedHashMap<>();
    document.put("attributes.view.id", s.getViewId());
    document.put("window_id", s.getWindowId());
    document.put("attributes.session.id", s.getSessionId());
    document.put("app_name", s.getAppName());
    document.put("resource.service.name", s.getServiceName());
    document.put("resource.service.version", s.getServiceVersion());
    document.put("resource.deployment.environment.name", s.getEnvironment());
    document.put("resource.telemetry.sdk.name", s.getSdkName());
    document.put("resource.telemetry.sdk.language", s.getSdkLang());
    document.put("resource.telemetry.sdk.version", s.getSdkVersion());
    document.put("bk_biz_id", s.getBkBizId());
    document.put("attributes.user.id", s.getUserId());
    document.put("resource.user_agent.name", s.getBrowser());
    document.put("resource.user_agent.version", s.getBrowserVersion());
    document.put("resource.user_agent.os.name", s.getOs());
    document.put("resource.device.type", s.getDeviceType());
    document.put("attributes.network.effective_type", s.getNetworkType());
    document.put("attributes.view.name", s.getViewName());
    document.put("attributes.view.url_template", s.getUrlTemplate());
    document.put("attributes.view.previous_url_template", s.getPreviousUrlTemplate());
    document.put("attributes.view.loading_type", s.getViewLoadingType());
    document.put("attributes.view.loading_time", s.getViewLoadingTime());
    document.put("view_loading_time_source", s.getViewLoadingTimeSource());
    document.put("end_reason", s.getViewEndReason());
    document.put("close_reason", s.getCloseReason());
    document.put("min_start_time", s.getMinStartTime());
    document.put("max_end_time", s.getMaxEndTime());
    document.put("action_count", s.getActionCount());
    document.put("resource_count", s.getResourceCount());
    document.put("error_count", s.getErrorCount());
    document.put("request_count", s.getRequestCount());
    document.put("request_error_count", s.getRequestErrorCount());
    document.put("long_task_count", s.getLongTaskCount());
    document.put("frustration_count", s.getFrustrationCount());
    document.put("version", s.getVersion());
    document.put("web_vitals.fcp", s.getFcp());
    document.put("web_vitals.cls", s.getCls());
    document.put("web_vitals.inp", s.getInp());
    document.put("web_vitals.lcp", s.getLcp());
    document.put("web_vitals.ttfb", s.getTtfb());
    document.put("updated_at_ts", s.getUpdatedAtTs());
    document.put("close_time", s.getClosTim());
    document.put("date", s.getDate());
    document.put("closed", s.isClosed());
    document.put("is_active", s.isActive());
    document.put("attributes.session.has_replay", s.isHasReplay());
    document.put("trace_count", s.getTraceCount());
    document.put("duration", s.getDuration());
    return document;
  }
}
