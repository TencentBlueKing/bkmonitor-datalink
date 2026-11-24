# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""UC 拉取进度和恢复判断脱离 ORM 后的行为回归。"""

import unittest
from unittest.mock import Mock, patch

from linkd_runtime.executor import Executor


class UCTest(unittest.TestCase):
    def setUp(self):
        self.request = {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "uc", "plugin": "uc", "mode": "pull", "received_at": "2026-11-14T12:00:00Z", "timezone": "UTC", "state": {"meta_info": 1700000000}}

    def test_empty_pull_returns_candidate_cursor_and_logs_out(self):
        api = Mock()
        api.pull_alarms.return_value = []
        with patch("linkd_runtime.plugins.uc.UCApi", return_value=api):
            result = Executor().execute(self.request)
        self.assertEqual(result["events"], [])
        self.assertGreater(result["proposed_state"]["meta_info"], 1700000000)
        self.assertEqual(self.request["state"]["meta_info"], 1700000000)
        self.assertEqual(api.pull_alarms.call_args.kwargs["time_from"], "2023-11-14 22:13")
        api.logout.assert_called_once()

    def test_recovery_preserves_identity_without_advancing_cursor(self):
        api = Mock()
        api.check_alarms.return_value = [{"serialNo": 7, "ackType": 1}]
        active = {"bk_tenant_id": "tenant", "source_id": "uc", "event_id": "7", "fingerprint": "7", "level": "warning"}
        reader = Mock(return_value=[active])
        with patch("linkd_runtime.plugins.uc.UCApi", return_value=api):
            result = Executor(active_alerts=reader).execute({**self.request, "mode": "recovery"})
        self.assertEqual(result["events"][0]["fingerprint"], "7")
        self.assertEqual(result["events"][0]["evaluations"][0]["action"], "resolved")
        self.assertNotIn("proposed_state", result)
        api.logout.assert_called_once()

    def test_pull_failure_still_logs_out(self):
        api = Mock()
        api.pull_alarms.side_effect = TimeoutError()
        with patch("linkd_runtime.plugins.uc.UCApi", return_value=api), self.assertRaises(TimeoutError):
            Executor().execute(self.request)
        api.logout.assert_called_once()
