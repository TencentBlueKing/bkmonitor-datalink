# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""原生监控来源辅助查询；数据接入仍由原生链路负责。"""

import copy
import json

from linkd_runtime.base import BaseClientV2


class AlarmClient(BaseClientV2):
    @staticmethod
    def get_custom_msg_fields():
        return ["close_reason"]

    def get_metric(self, alarm, start, end):
        source = self.alarm_source_obj
        reader = source.metrics
        if reader is None:
            raise RuntimeError("metric query capability is not configured")
        alarm = copy.deepcopy(alarm)
        native = alarm.get("source_id") in ("built_in_bk", "built_in_bk_" + source.bk_tenant_id)
        if native:
            if alarm.get("recovery_record_id"):
                recovered = reader.recovery_fields(source.bk_tenant_id, alarm["alarm_id"], alarm["recovery_record_id"])
                recovered = copy.deepcopy(recovered)
                recovered["event_id"] = alarm["event_id"]
                alarm = recovered
            # None 才表示无历史执行引用；查询失败必须传播，不能回退旧参数。
            historical = reader.query_builtin(source.bk_tenant_id, alarm, start, end)
            if historical is not None:
                return historical
        groups = alarm.get("metric_group_params")
        if groups:
            params = json.loads(groups)
        else:
            keys = ("model_id", "model_inst_id", "metric_name", "Namespace", "item", "unit", "aggregate_func", "result_table_id", "where_condition")
            params = [{**{key: alarm.get(key) for key in keys}, "metric_query_params": alarm.get("metric_query_params") or "{}"}]
        result = []
        for item in params:
            item = dict(item)
            query = {**item, **json.loads(item.pop("metric_query_params", "{}"))}
            query.update(start_time=start.strftime("%Y-%m-%d %H:%M:%S"), end_time=end.strftime("%Y-%m-%d %H:%M:%S"), bk_username=self.alarm_source_config_dict["username"])
            result.append(reader.query_points(source.bk_tenant_id, query))
        return result
