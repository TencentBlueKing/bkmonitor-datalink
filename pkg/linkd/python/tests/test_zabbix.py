# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Zabbix 原始输入、类型规则与恢复身份。"""

import base64
import json
import unittest

from linkd_runtime.executor import Executor


class ZabbixTest(unittest.TestCase):
    def payload(self):
        return {"ip": "10.0.0.1", "source_id": "trigger-123", "source_time": "2026-11-14T12:00:00Z", "alarm_type": "cpu.high", "alarm_content": "CPU", "alarm_raw": json.dumps({"TRIGGER.NAME": "CPU", "ITEM.VALUE": "90", "TRIGGER.SEVERITY": "High"}), "event_status": "PROBLEM"}

    def execute(self, data, rules=None):
        return Executor().execute({"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "zabbix", "plugin": "zabbix", "mode": "push", "configs": {"alarm_types": rules or []}, "data": data})["events"][0]

    def test_trigger_and_recovery(self):
        data = self.payload()
        trigger = self.execute(data)
        recovery = self.execute({**data, "event_status": "RESOLVED"})
        self.assertEqual(trigger["fingerprint"], "123-10.0.0.1")
        self.assertEqual(trigger["fingerprint"], recovery["fingerprint"])
        self.assertEqual(trigger["evaluations"], [{"action": "triggered", "severity": "fatal"}])
        self.assertEqual(recovery["evaluations"][0]["action"], "resolved")
        self.assertIsInstance(data["alarm_raw"], str)

    def test_rules_preserve_order_and_default(self):
        rules = [{"name": "exact", "match_mode": "string", "match_patten": "cpu.high"}, {"name": "regex", "match_mode": "regular", "match_patten": "^cpu"}, {"name": "wild", "match_mode": "wildcard", "match_patten": "cpu.*"}]
        self.assertEqual(self.execute(self.payload(), rules)["extra_data"]["item"], "exact,regex,wild")
        self.assertEqual(self.execute(self.payload())["extra_data"]["item"], "cpu.high")

    def test_base64(self):
        data = self.payload()
        data["format"] = "base64"
        for key in ("alarm_content", "alarm_raw"):
            data[key] = base64.b64encode(data[key].encode()).decode()
        self.assertEqual(self.execute(data)["title"], "CPU")

    def test_bad_inputs_fail(self):
        for data in ([], {}, {**self.payload(), "alarm_raw": "not JSON"}):
            with self.subTest(data_type=type(data).__name__), self.assertRaises(ValueError):
                self.execute(data)
