# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""自定义插件清单加载及真实执行进程验证。"""

import json
from pathlib import Path
import tempfile
import unittest

from linkd_runtime.executor import ExecutorFactory
from linkd_runtime.loader import load_plugin, read_manifest
from linkd_runtime.process_executor import ProcessExecutor


class LoaderTest(unittest.TestCase):
    def test_manifest_plugin_executes_in_worker(self):
        root = Path(__file__).resolve().parents[1]
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder)
            (path / "plugin.py").write_text((root / "examples/push_plugin.py").read_text())
            (path / "manifest.json").write_text(json.dumps({"demo": "plugin.py"}))
            files = read_manifest(path / "manifest.json")
            executor = ProcessExecutor(timeout=2, factory=ExecutorFactory(plugin_files=files))
            try:
                request = json.loads((root / "examples/push_request.json").read_text())
                result = executor.execute(request)
                self.assertEqual(result["events"][0]["fingerprint"], "alert-1")
            finally:
                executor.close()

    def test_invalid_plugin_is_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "bad.py"
            path.write_text('MODES = ("push",)\nclass AlarmClient: pass\n')
            with self.assertRaises(ValueError):
                load_plugin("bad", path)
            executor = ProcessExecutor(timeout=2, factory=ExecutorFactory(plugin_files={"bad": str(path)}))
            try:
                with self.assertRaisesRegex(RuntimeError, "plugin initialization failed"):
                    executor.start()
                self.assertIsNone(executor.process)
            finally:
                executor.close()

    def test_relative_modules_and_data_files_are_isolated_in_worker(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            files = {}
            for name in ("alpha", "beta"):
                path = root / name
                path.mkdir()
                (path / "helpers.py").write_text('from pathlib import Path\nimport json\nTITLE = json.loads(Path(__file__).with_name("message.json").read_text())["title"]\n')
                (path / "message.json").write_text(json.dumps({"title": name}))
                (path / "plugin.py").write_text('''from linkd_runtime.base import BaseClientV2
from .helpers import TITLE
MODES = ("push",)
class AlarmClient(BaseClientV2):
    def __init__(self, source, data):
        super().__init__(source)
    def pull_raw_events(self):
        return [{"fingerprint": "original", "title": TITLE, "evaluations": [{"action": "triggered", "severity": "warning"}]}]
''')
                files[name] = str(path / "plugin.py")
            executor = ProcessExecutor(timeout=2, factory=ExecutorFactory(plugin_files=files))
            try:
                for name in ("alpha", "beta", "alpha"):
                    result = executor.execute({"version": 1, "request_id": name, "bk_tenant_id": "tenant", "source_id": "source", "plugin": name, "mode": "push", "data": {}})
                    self.assertEqual(result["events"][0]["title"], name)
            finally:
                executor.close()

    def test_failed_import_cleans_attachment_namespace(self):
        import sys
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "helper.py").write_text("VALUE = 1\n")
            path = root / "plugin.py"
            path.write_text('from .helper import VALUE\nraise RuntimeError("initialization failed")\n')
            with self.assertRaises(RuntimeError):
                load_plugin("failed_bundle", path)
            self.assertFalse(any(name == "linkd_user_plugin_failed_bundle" or name.startswith("linkd_user_plugin_failed_bundle.") for name in sys.modules))
