# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""最小推送示例；来源编号作为稳定 fingerprint。"""

from linkd_runtime.base import BaseClientV2

MODES = ("push",)


class AlarmClient(BaseClientV2):
    def __init__(self, source, data):
        super().__init__(source)
        self.data = data

    def pull_raw_events(self):
        return [{
            "fingerprint": str(self.data["id"]),
            "title": self.data["title"],
            "evaluations": [{"action": "triggered", "severity": "warning"}],
            "occurred_at": self.source_time(self.data["time"]),
        }]
