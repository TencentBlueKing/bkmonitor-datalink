"""离线执行指定 bk-monitor 源码的真实阈值/Ping 描述逻辑。

仅提取不访问基础设施的 serializer、检测类、单位与模板函数；DataPoint 和
AnomalyDataPoint 使用最小信封适配器。不会加载 Django 业务 app、Redis 或数据库。
这是源码级对照，不是 bk-monitor 服务端到端验证。Python/Django 仅用于生成测试材料，
Linkd 的构建和运行不依赖它们。调用方必须传入已核对版本的 bk-monitor 仓库。
"""

import ast
import importlib.util
import inspect
import logging
import sys
import types
from collections import OrderedDict
from pathlib import Path
from typing import final

import django
import six
from django.conf import settings
from django.template import Context, Template
from django.utils.safestring import mark_safe
from rest_framework import serializers


def _definitions(path, names, namespace):
    tree = ast.parse(path.read_text(), filename=str(path))
    selected = []
    for node in tree.body:
        name = getattr(node, "name", None)
        if isinstance(node, ast.Assign):
            name = node.targets[0].id if isinstance(node.targets[0], ast.Name) else None
        if name in names:
            selected.append(node)
    if len(selected) != len(names):
        raise RuntimeError("expected upstream definitions are missing")
    exec(compile(ast.Module(body=selected, type_ignores=[]), str(path), "exec"), namespace)


class _DataPoint:
    context_field = ("value", "unit", "item", "timestamp", "record_id")

    def __init__(self, value, unit, name):
        self.value, self.unit = value, unit
        self.item = types.SimpleNamespace(name=name, id=1, strategy=types.SimpleNamespace(id=1))
        self.timestamp, self.record_id = 1, "test.1"

    def as_dict(self):
        return {}


class _Anomaly:
    def __init__(self, data_point, detector):
        self.data_point, self.detector = data_point, detector
        self.child_detector = []


def load_oracle(repository):
    root = Path(repository)
    backend = root / "bkmonitor"
    if not settings.configured:
        settings.configure(
            POINT_PRECISION=6, USE_I18N=False, USE_L10N=False,
            INSTALLED_APPS=[],
            TEMPLATES=[{"BACKEND": "django.template.backends.django.DjangoTemplates",
                        "OPTIONS": {"libraries": {"unit": "linkd_oracle_unit"}}}],
        )
        django.setup()
    package = types.ModuleType("linkd_oracle_units")
    package.__path__ = [str(backend / "core/unit")]
    sys.modules[package.__name__] = package
    for name in ("models", "init_data"):
        spec = importlib.util.spec_from_file_location(package.__name__ + "." + name,
                                                     backend / "core/unit" / (name + ".py"))
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
    models = sys.modules[package.__name__ + ".models"]
    namespace = {
        "OrderedDict": OrderedDict, "six": six,
        "define": sys.modules[package.__name__ + ".init_data"].define,
        "UnitMeta": models.UnitMeta, "create_default_unit": models.create_default_unit,
        "MetricUnitIdInvalid": ValueError, "MetricUnitCategoryNotExist": ValueError,
        "UNITS": None,
    }
    _definitions(backend / "core/unit/base.py", {"load_units", "load_unit", "setup"}, namespace)
    namespace["setup"]()
    unit_module = types.ModuleType("linkd_oracle_unit")
    unit_module.register = django.template.Library()
    unit_module.settings, unit_module.load_unit = settings, namespace["load_unit"]
    _definitions(backend / "alarm_backends/templatetags/unit.py",
                 {"unit_auto_convert", "unit_convert_min", "unit_suffix"}, unit_module.__dict__)
    sys.modules[unit_module.__name__] = unit_module
    namespace.update({
        "serializers": serializers, "final": final, "inspect": inspect,
        "settings": settings, "Context": Context, "Template": Template,
        "_": lambda value: value, "mark_safe": mark_safe,
        "DataPoint": _DataPoint, "AnomalyDataPoint": _Anomaly,
        "InvalidDataPoint": ValueError, "InvalidAlgorithmsConfig": ValueError,
        "InvalidThresholdConfig": ValueError, "logger": logging.getLogger("linkd-oracle"),
        "unit_auto_convert": unit_module.unit_auto_convert,
        "unit_convert_min": unit_module.unit_convert_min,
    })
    _definitions(root / "bk-monitor-base/src/bk_monitor_base/domains/strategy/serializers.py",
                 {"THRESHOLD_ALLOWED_METHODS", "ThresholdSerializer"}, namespace)
    _definitions(backend / "alarm_backends/service/detect/strategy/__init__.py",
                 {"DetectContext", "Algorithms", "ExprDetectAlgorithms", "BasicAlgorithmsCollection"}, namespace)
    _definitions(backend / "alarm_backends/service/detect/strategy/threshold.py",
                 {"AndThreshold", "Threshold"}, namespace)
    _definitions(backend / "alarm_backends/service/detect/strategy/ping_unreachable.py",
                 {"PingUnreachable"}, namespace)

    def describe(value, unit, name, algorithms, connector="and"):
        point = _DataPoint(value, unit, name)
        descriptions = []
        for algorithm in algorithms:
            kind = algorithm["type"]
            if kind not in ("Threshold", "PingUnreachable"):
                raise ValueError("oracle algorithm is not supported")
            detector = namespace[kind](algorithm.get("config", []), algorithm.get("unit_prefix", ""))
            detected = detector.detect(point)
            if not detected:
                if connector == "and":
                    return None
                continue
            # 按真实 gen_anomaly_point 拼接同一算法内的条件，最后跨算法按“且”组合。
            descriptions.append(detector.gen_anomaly_point(point, detected, 1).anomaly_message)
        if not descriptions:
            return None
        if len(descriptions) == 1:
            return descriptions[0]
        # 基础检测类先组合子算法描述，再只加一次指标前后缀。
        parts = []
        for algorithm in algorithms:
            detector = namespace[algorithm["type"]](algorithm.get("config", []), algorithm.get("unit_prefix", ""))
            detected = detector.detect(point)
            if detected:
                parts.extend(result.anomaly_message for result in detected)
        prefix, suffix = detector.anomaly_message_template_tuple(point)
        return prefix + "且".join(parts) + suffix

    return describe
