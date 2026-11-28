# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""无插件 HTTP 推送的原生 standard 接入。"""

from linkd_runtime.base import BaseClientV2


class AlarmClient(BaseClientV2):
    def __init__(self, alarm_source_obj, data):
        super().__init__(alarm_source_obj)
        self.data = data

    def pull_raw_events(self):
        events = self.data if isinstance(self.data, list) else [self.data]
        return [self._normalize_resource(event) for event in events]

    @staticmethod
    def _normalize_resource(event):
        """旧顶层资源字段归入 standard 扩展；身份查询和优先级由 Linkd resource 处理。"""
        if not isinstance(event, dict) or not any(key in event for key in ("bk_obj_id", "bk_inst_id")):
            return event
        normalized = dict(event)
        extra = normalized.get("extra_data", {})
        if not isinstance(extra, dict):
            raise ValueError("REST API extra_data 必须为对象")
        extra = dict(extra)
        for key in ("bk_obj_id", "bk_inst_id"):
            if key not in normalized:
                continue
            value = normalized.pop(key)
            if key == "bk_inst_id":
                if isinstance(value, bool) or not isinstance(value, (str, int)):
                    raise ValueError("REST API bk_inst_id 必须为字符串或整数")
                value = str(value)
            elif not isinstance(value, str):
                raise ValueError("REST API bk_obj_id 必须为字符串")
            if key in extra:
                previous = extra[key]
                if key == "bk_inst_id" and isinstance(previous, (str, int)) and not isinstance(previous, bool):
                    previous = str(previous)
                if type(previous) is not type(value) or previous != value:
                    raise ValueError(f"REST API {key} 与 extra_data 中的身份冲突")
            extra[key] = value
        normalized["extra_data"] = extra
        return normalized
