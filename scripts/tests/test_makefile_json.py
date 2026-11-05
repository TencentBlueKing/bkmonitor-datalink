#!/usr/bin/env python3
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import os
import re
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


class ModuleJSONBuildTest(unittest.TestCase):
    def build_arguments(self, module, *arguments, json_env=None, root_target=False):
        env = os.environ.copy()
        env.pop("JSON_LIB", None)
        if json_env is not None:
            env["JSON_LIB"] = json_env
        target = [module] if root_target else [f"MODULE={module}", "build"]
        result = subprocess.run(
            ["make", "--no-print-directory", "-n", *target, *arguments],
            cwd=ROOT,
            env=env,
            text=True,
            capture_output=True,
            check=True,
        )
        return re.search(r"JSON_LIB=([^ ]*) build", result.stdout).group(1)

    def test_uq_and_bmw_keep_module_sonic_default(self):
        for module in ("unify-query", "bk-monitor-worker"):
            with self.subTest(module=module):
                self.assertEqual(self.build_arguments(module), "jsonsonic")
                self.assertEqual(
                    self.build_arguments(module, root_target=True), "jsonsonic"
                )

    def test_explicit_fallback_and_environment_remain_available(self):
        for module in ("unify-query", "bk-monitor-worker"):
            with self.subTest(module=module):
                self.assertEqual(self.build_arguments(module, "JSON_LIB="), "")
                self.assertEqual(
                    self.build_arguments(module, "JSON_LIB=", root_target=True), ""
                )
                self.assertEqual(self.build_arguments(module, json_env=""), "")
                self.assertEqual(
                    self.build_arguments(module, json_env="custom-tag"), "custom-tag"
                )

    def test_other_modules_keep_existing_root_argument(self):
        self.assertEqual(self.build_arguments("transfer"), "")


class CollectorNodemanV3BuildTest(unittest.TestCase):
    def make(self, *arguments, cwd=ROOT):
        env = os.environ.copy()
        env.pop("JSON_LIB", None)
        return subprocess.run(
            ["make", "--no-print-directory", *arguments],
            cwd=cwd,
            env=env,
            text=True,
            capture_output=True,
            check=True,
        ).stdout

    def test_build_outputs_v2_and_v3_packages_to_release_path(self):
        commands = self.make(
            "-n", "RELEASE_PATH=/release", "VERSION=1.2.3", "build", cwd=ROOT / "pkg" / "collector"
        )

        v2_tar = commands.index("tar -zcvf bk-collector-1.2.3.tgz --remove-files -C . *")
        v3_package = commands.index("./control.sh package_v3 linux amd64 x86_64 1.2.3 ")
        v3_tar = commands.index("tar -zcvf /release/bk-collector-nodeman-v3-1.2.3.tgz -C ")
        # V3 须在 V2 打包之后生成，否则会被打进 V2 插件包
        self.assertLess(v2_tar, v3_package)
        self.assertLess(v3_package, v3_tar)


if __name__ == "__main__":
    unittest.main()
