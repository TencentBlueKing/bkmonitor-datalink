# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http:#opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""验证可选元信息字段不会改变存量配置的渲染字节。"""

import hashlib
import unittest
from pathlib import Path

import yaml
from jinja2 import Environment

TEMPLATES = Path(__file__).resolve().parents[1] / "templates/linux/x86_64/etc"
BASE = {"bk_data_token": "demo", "bk_biz_id": 7, "bk_app_name": "svc"}


def render(kind: str, context: dict, trim: bool = False) -> str:
    env = Environment(trim_blocks=trim, lstrip_blocks=trim)
    source = (TEMPLATES / f"bk-collector-{kind}.conf.tpl").read_text()
    return env.from_string(source).render(**BASE, **context)


class MetadataTemplateTests(unittest.TestCase):
    def test_existing_bytes(self) -> None:
        # 两种 Jinja 换行模式；基准来自增加可选字段前的 1312975d。
        cases = [
            ("application", {}, "ec184123fe1ce68e18c1406467ffc614aefbcb46a865222c950a5b18b403b1cc",
             "6da846dff3212e68e59b1466debff0cce7a4fea00e91b55840f3beba2b05645d"),
            ("application", {"queue_config": {"metrics_batch_size": 10}},
             "89826a9743db39799cc8da66eee327d9bc412c06555b30c9c3a12bae32894ca0",
             "ff13e7447b5f9663ef5d4fe8225416bfbfdfe269f7c62dc5184795fb0c2b9073"),
            ("platform", {"qps_config": {"name": "rate_limiter/token_bucket", "type": "token_bucket", "qps": 123}},
             "c102ade7dd4531971d3320d75fdda2c61bd762638a96e3b144501f335a89ebf1",
             "f33cda7fab3fc5bc47c2f46a8fa8951e7a6b2d58344ca21b0fc801c6d216f3be"),
        ]
        for kind, context, default, trimmed in cases:
            for trim, expected in [(False, default), (True, trimmed)]:
                with self.subTest(kind=kind, trim=trim, context=context):
                    self.assertEqual(hashlib.sha256(render(kind, context, trim).encode()).hexdigest(), expected)

    def test_missing_or_null_settings_are_identical(self) -> None:
        for trim in [False, True]:
            for kind in ["application", "report-v2", "platform"]:
                for empty in [None, {}, {"enabled": False}]:
                    self.assertEqual(render(kind, {}, trim), render(kind, {"metric_metadata": empty}, trim))

    def test_platform_target(self) -> None:
        metadata = {
            "enabled": True, "dataid": 901, "rate_limit": {"instance_per_minute": 30},
            "exporter": {"interval": "30s", "timeout": "2s", "batch_size": 2, "queue_size": 10},
        }
        for trim in [False, True]:
            result = render("platform", {"metric_metadata": metadata}, trim)
            self.assertEqual(yaml.safe_load(result)["exporter"]["metric_metadata"], metadata)
