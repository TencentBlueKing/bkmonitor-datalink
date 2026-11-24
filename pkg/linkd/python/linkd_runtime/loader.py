# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""从可信部署清单加载插件；HTTP 请求不能指定文件路径。"""

import importlib.util
import json
from pathlib import Path
import sys

from .base import BaseClientV2


def read_manifest(path):
    """清单为 name→Python 文件路径，路径相对清单目录解析。"""
    file = Path(path).resolve()
    entries = json.loads(file.read_text(encoding="utf-8"))
    if not isinstance(entries, dict):
        raise ValueError("plugin manifest must be an object")
    return {name: str((file.parent / relative).resolve()) for name, relative in entries.items()}


def load_plugin(name, path):
    """初始化阶段加载插件包，支持相对导入附件；不允许执行中并发重载。"""
    from .executor import Plugin

    module_name = "linkd_user_plugin_" + name
    _clear_modules(module_name)
    path = Path(path).resolve()
    # 每个插件使用自己的包命名空间，不能把目录加入全局 sys.path 后共享 helper 名称。
    spec = importlib.util.spec_from_file_location(module_name, path, submodule_search_locations=[str(path.parent)])
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    try:
        spec.loader.exec_module(module)
        client = module.AlarmClient
        if not isinstance(client, type) or not issubclass(client, BaseClientV2):
            raise ValueError("AlarmClient must inherit BaseClientV2")
        modes = tuple(module.MODES)
        if not modes or any(mode not in ("push", "pull", "recovery", "metric", "fields") for mode in modes):
            raise ValueError("invalid plugin modes")
        return Plugin(client, modes)
    except BaseException:
        _clear_modules(module_name)
        raise


def _clear_modules(module_name):
    """清理一次初始化留下的入口及附件模块，避免失败重载复用旧代码。"""
    for loaded in tuple(sys.modules):
        if loaded == module_name or loaded.startswith(module_name + "."):
            sys.modules.pop(loaded, None)
