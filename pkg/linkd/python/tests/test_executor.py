# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""请求分发协议的模式、实例和租户边界。"""

import unittest

from linkd_runtime.base import BaseClientV2
from linkd_runtime.executor import Executor, Plugin


class PullPlugin(BaseClientV2):
    def pull_raw_events(self):
        return []

    def pull_recovery_events(self):
        return [{"fingerprint": "a", "title": "恢复", "evaluations": [{"action": "resolved", "severity": "warning"}]}]


class ExecutorTest(unittest.TestCase):
    def request(self, **changes):
        return {"version": 1, "request_id": "r1", "bk_tenant_id": "a", "source_id": "s", "plugin": "rest_api_push", "mode": "push", "data": {"action": "heartbeat"}, **changes}

    def test_push_empty_result_preserves_identity(self):
        result = Executor().execute(self.request())
        self.assertEqual(result, {"version": 1, "request_id": "r1", "bk_tenant_id": "a", "source_id": "s", "events": []})

    def test_pull_and_recovery(self):
        executor = Executor({"pull": Plugin(PullPlugin, ("pull", "recovery"))})
        self.assertEqual(executor.execute(self.request(plugin="pull", mode="pull"))["events"], [])
        result = executor.execute(self.request(plugin="pull", mode="recovery"))
        self.assertEqual(result["events"][0]["evaluations"][0]["action"], "resolved")

    def test_invalid_requests(self):
        for changes in ({"version": 2}, {"request_id": ""}, {"bk_tenant_id": ""}, {"plugin": "missing"}, {"mode": "pull"}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                Executor().execute(self.request(**changes))

    def test_instance_is_not_shared(self):
        class Once(PullPlugin):
            def pull_raw_events(self):
                if hasattr(self, "used"):
                    raise RuntimeError("reused instance")
                self.used = True
                return super().pull_recovery_events()
        executor = Executor({"pull": Plugin(Once, ("pull",))})
        for tenant in ("a", "b"):
            result = executor.execute(self.request(plugin="pull", mode="pull", bk_tenant_id=tenant))
            self.assertEqual(result["events"][0]["bk_tenant_id"], tenant)

    def test_plugin_identity_does_not_change_query_scope(self):
        seen = []
        class MigratedPlugin(BaseClientV2):
            def pull_raw_events(self):
                self.query_alarm_source_not_close_alarm(self.alarm_source_obj)
                return [{"fingerprint": self.alarm_source_id + ":alarm", "title": "test", "evaluations": [{"action": "triggered", "severity": "warning"}]}]
        def reader(tenant, source, query):
            seen.append((tenant, source))
            return []
        executor = Executor({"migrated": Plugin(MigratedPlugin, ("pull",))}, active_alerts=reader)
        result = executor.execute(self.request(plugin="migrated", mode="pull", plugin_source_id="original-kac-id"))
        self.assertEqual(result["source_id"], "s")
        self.assertEqual(result["events"][0]["fingerprint"], "original-kac-id:alarm")
        self.assertEqual(seen, [("a", "s")])
        fallback = executor.execute(self.request(plugin="migrated", mode="pull"))
        self.assertEqual(fallback["events"][0]["fingerprint"], "s:alarm")

    def test_push_heartbeat_is_filtered_before_plugin(self):
        seen = []
        class PushPlugin(BaseClientV2):
            def __init__(self, source, data):
                super().__init__(source)
                seen.append(data)
            def pull_raw_events(self):
                return []
        executor = Executor({"push": Plugin(PushPlugin, ("push",))})
        for payload in ({"action": "heartbeat"}, [{"action": "heartbeat"}], [{"action": "heartbeat"}, {"action": "heartbeat"}]):
            result = executor.execute(self.request(plugin="push", data=payload))
            self.assertEqual(result["events"], [])
            self.assertNotIn("proposed_state", result)
        self.assertEqual(seen, [])
        first, second = {"id": "first"}, {"nested": {"action": "heartbeat"}}
        executor.execute(self.request(plugin="push", data=[first, {"action": "heartbeat"}, second]))
        self.assertEqual(seen, [[first, second]])
        executor.execute(self.request(plugin="push", data=first))
        self.assertEqual(seen[-1], first)
        executor.execute(self.request(plugin="push", data=[]))
        self.assertEqual(seen[-1], [])

    def test_heartbeat_does_not_bypass_plugin_selection(self):
        with self.assertRaises(ValueError):
            Executor().execute(self.request(plugin="missing", data={"action": "heartbeat"}))

    def test_all_builtin_message_fields_without_business_input(self):
        from linkd_runtime.executor import builtin_plugins
        executor = Executor()
        for name in builtin_plugins():
            with self.subTest(plugin=name):
                request = self.request(plugin=name, mode="fields")
                request.pop("data")
                result = executor.execute(request)
                self.assertEqual(result["data"], ["close_reason"] if name == "bs_monitor" else [])
                self.assertNotIn("events", result)
                self.assertNotIn("proposed_state", result)

    def test_custom_fields_method_is_preserved(self):
        class FieldsPlugin(BaseClientV2):
            @staticmethod
            def get_custom_msg_fields():
                return ["vendor_status", "device_name"]
        executor = Executor({"custom": Plugin(FieldsPlugin, ("fields",))})
        result = executor.execute(self.request(plugin="custom", mode="fields"))
        self.assertEqual(result["data"], ["vendor_status", "device_name"])

    def test_builtin_without_metric_returns_legacy_empty_object(self):
        from linkd_runtime.executor import builtin_plugins
        executor = Executor()
        for name in builtin_plugins():
            if name == "bs_monitor":
                continue
            with self.subTest(plugin=name):
                result = executor.execute(self.request(plugin=name, mode="metric", data={"alarm": {}, "start_time": "2026-11-14T12:00:00Z", "end_time": "2026-11-14T13:00:00Z"}))
                self.assertEqual(result["data"], {})
                self.assertNotIn("events", result)
