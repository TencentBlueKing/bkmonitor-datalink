# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""eSight 固定时间窗口与分页错误传播。"""

import unittest
from unittest.mock import patch

from linkd_runtime.executor import Executor
from linkd_runtime.plugins.esight import get_alarm


class ESightTest(unittest.TestCase):
    def test_pages_use_fixed_window(self):
        with patch("linkd_runtime.plugins.esight._request", side_effect=[{"data": [{"id": 1}], "totalPage": 2}, {"data": [{"id": 2}]}]) as call:
            result = get_alarm("host", "openid", 5, "2026-11-14T12:00:00Z")
        self.assertEqual(result["message"], [{"id": 1}, {"id": 2}])
        first, second = [c.kwargs["params"] for c in call.call_args_list]
        self.assertEqual(first["endTime"], second["endTime"])
        self.assertEqual([first["pageNo"], second["pageNo"]], [1, 2])

    def test_later_page_error_is_not_partial_success(self):
        with patch("linkd_runtime.plugins.esight._request", side_effect=[{"data": [{}], "totalPage": 2}, ValueError("denied")]), self.assertRaises(ValueError):
            get_alarm("host", "openid", 5, "2026-11-14T12:00:00Z")

    def test_pull_preserves_recovery_action(self):
        request = {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "esight", "plugin": "esight", "mode": "pull", "received_at": "2026-11-14T12:00:00Z", "configs": {"host": "host"}}
        event = {"moName": "device", "eventType": "fault", "perceivedSeverity": 2, "cleared": True, "latestLogTime": "2026-11-14T11:00:00Z", "alarmName": "故障"}
        with patch("linkd_runtime.plugins.esight._request", side_effect=[{"data": "token"}, {"data": [event], "totalPage": 1}]):
            result = Executor().execute(request)
        self.assertEqual(result["events"][0]["evaluations"], [{"action": "resolved", "severity": "warning"}])
        self.assertEqual(result["events"][0]["fingerprint"], "device-fault-2")
