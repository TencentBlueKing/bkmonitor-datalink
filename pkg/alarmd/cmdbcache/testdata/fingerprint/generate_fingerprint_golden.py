"""Regenerate fingerprint_golden.json from the platform's own fingerprint code.

usage: python3 -I generate_fingerprint_golden.py <bkmonitor-dir> > fingerprint_golden.json

python3 is 3.10 or later: the platform's code uses "X | Y" type unions.

<bkmonitor-dir> is the bkmonitor/ directory of a bk-monitor checkout at the
deployed revision, commit 2fc04dd71dac153177ce7ffcdc64511e08a406fc. Nothing
of it is imported: the functions the alert fingerprint is computed by are
read out of their files and run on their own -

- MonitorEventAdapter.extract_target (alarm_backends/core/alert/adapter.py),
  which picks the alert's target and the dimensions it is deduplicated by;
- Event._clean_target_type, _clean_dedupe_keys, _clean_tags,
  cal_dedupe_md5, get_field, get_tags_value and tags_dict
  (alarm_backends/core/alert/event.py), which build the dedupe values;
- count_md5 and _count_md5 (bkmonitor/utils/common_utils.py), and the
  constants they read.

Each case is a record's dimensions as TopoNodeFuller.full
(alarm_backends/service/access/data/fullers.py) leaves them, and the
strategy's agg_dimension; the golden records the dedupe md5 and the
bk_target_cloud_id the record carries. alarmd is held to both.
"""

import ast
import copy
import hashlib
import json
import os
import sys

ROOT = sys.argv[1]


def source(path):
    with open(os.path.join(ROOT, path), encoding="utf-8") as handle:
        return ast.parse(handle.read())


def functions(tree, names, cls=None):
    """The named function definitions, top-level or in the named class."""
    found = {}
    scopes = [tree]
    if cls is not None:
        scopes = [node for node in ast.walk(tree) if isinstance(node, ast.ClassDef) and node.name == cls]
    for scope in scopes:
        for node in scope.body:
            if isinstance(node, ast.FunctionDef) and node.name in names:
                node.decorator_list = []
                found[node.name] = node
    missing = set(names) - set(found)
    if missing:
        raise SystemExit(f"not found: {sorted(missing)}")
    return [found[name] for name in names]


def assignments(tree, names):
    return [node for node in tree.body if isinstance(node, ast.Assign)
            and any(isinstance(target, ast.Name) and target.id in names for target in node.targets)]


def classes(tree, names):
    return [node for node in tree.body if isinstance(node, ast.ClassDef) and node.name in names]


namespace = {"copy": copy, "json": json, "hashlib": hashlib, "inspect": None,
             "NO_DATA_TAG_DIMENSION": "__NO_DATA_DIMENSION__"}


class _Labels:
    EVENT = "event"
    BK_MONITOR_COLLECTOR = "bk_monitor"


class _Unreached:
    """A dependency of a branch none of the cases reaches."""

    def __getattr__(self, name):
        raise SystemExit(f"unexpected branch: {name}")


namespace.update({"DataTypeLabel": _Labels, "DataSourceLabel": _Labels, "ApmAlertHelper": _Unreached(),
                  "EventStatus": _Unreached(), "make_callable_hash": _Unreached()})

constants_alert = source("constants/alert.py")
module = ast.Module(body=classes(constants_alert, {"EventTargetType", "K8STargetType", "APMTargetType"})
                    + assignments(constants_alert, {"EVENT_EXTRA_TARGET_TYPE"})
                    + assignments(source("alarm_backends/constants.py"), {"DEFAULT_DEDUPE_FIELDS"})
                    + functions(source("bkmonitor/utils/common_utils.py"), ["_count_md5", "count_md5"]),
                    type_ignores=[])
exec(compile(ast.fix_missing_locations(module), "platform", "exec"), namespace)

adapter = ast.Module(body=functions(source("alarm_backends/core/alert/adapter.py"), ["extract_target"],
                                    cls="MonitorEventAdapter"), type_ignores=[])
exec(compile(ast.fix_missing_locations(adapter), "adapter", "exec"), namespace)

event_methods = functions(source("alarm_backends/core/alert/event.py"),
                          ["_clean_target_type", "_clean_dedupe_keys", "_clean_tags", "cal_dedupe_md5", "get_field",
                           "get_tags_value", "tags_dict"], cls="Event")
event_class = ast.ClassDef(name="Event", bases=[], keywords=[], body=event_methods, decorator_list=[])
for method in event_methods:
    if method.name == "tags_dict":
        method.decorator_list = [ast.Name(id="property", ctx=ast.Load())]
exec(compile(ast.fix_missing_locations(ast.Module(body=[event_class], type_ignores=[])), "event", "exec"), namespace)


def fingerprint(strategy_id, business_id, agg_dimension, dimensions):
    strategy = {"id": strategy_id, "bk_biz_id": business_id,
                "items": [{"query_configs": [{"agg_dimension": agg_dimension, "data_type_label": "time_series",
                                              "data_source_label": "bk_monitor"}]}]}
    target_type, target, data_dimensions = namespace["extract_target"](None, strategy, copy.deepcopy(dimensions))
    data_dimensions.pop("__additional_dimensions", {})
    event = namespace["Event"]()
    event.data = {"strategy_id": strategy_id, "target_type": target_type, "target": target, "bk_biz_id": business_id,
                  "tags": [{"key": key, "value": value} for key, value in data_dimensions.items()],
                  "dedupe_keys": [f"tags.{key}" for key in data_dimensions.keys()]}
    event.data["target_type"] = event._clean_target_type()
    event.data["dedupe_keys"] = event._clean_dedupe_keys()
    event.data["tags"] = event._clean_tags()
    event.dedupe_keys = event.data["dedupe_keys"]
    event.cal_dedupe_md5()
    return target_type, target, event.data["dedupe_md5"]


# The record is the series a service instance's process reports, its own
# address another than the instance's host's; the fuller has written the
# instance's address and cloud over it (fullers.py:86-87), the cloud as the
# cache holds it, a number. The host-branch case has the same record placed by
# its agent, the cloud written as str() (fullers.py:69-70).
RECORD = {"bk_target_service_instance_id": "7", "bk_agent_id": "agent-live", "device": "sda"}
INSTANCE_PLACED = {**RECORD, "bk_target_ip": "192.0.2.172", "bk_target_cloud_id": 0}
HOST_PLACED = {**RECORD, "bk_target_ip": "192.0.2.172", "bk_target_cloud_id": "0", "bk_host_id": "720002"}
CASES = [
    ("instance placed, host target", ["bk_target_ip", "bk_target_cloud_id", "device"], INSTANCE_PLACED),
    ("instance placed, cloud as a string", ["bk_target_ip", "bk_target_cloud_id", "device"],
     {**INSTANCE_PLACED, "bk_target_cloud_id": "0"}),
    ("instance placed, cloud in the tags", ["bk_target_cloud_id", "bk_target_service_instance_id", "device"],
     INSTANCE_PLACED),
    ("instance placed, cloud in the tags as a string", ["bk_target_cloud_id", "bk_target_service_instance_id", "device"],
     {**INSTANCE_PLACED, "bk_target_cloud_id": "0"}),
    ("instance placed, service target", ["bk_target_service_instance_id", "device"], INSTANCE_PLACED),
    ("host placed by agent, host target", ["bk_target_ip", "bk_target_cloud_id", "device"], HOST_PLACED),
]

out = []
for name, agg_dimension, dimensions in CASES:
    target_type, target, dedupe_md5 = fingerprint(101, 2, agg_dimension, dimensions)
    out.append({"name": name, "strategy_id": 101, "bk_biz_id": 2, "agg_dimension": agg_dimension,
                "dimensions": dimensions, "target_type": target_type, "target": target, "dedupe_md5": dedupe_md5,
                "bk_target_cloud_id": json.dumps(dimensions["bk_target_cloud_id"])})
json.dump(out, sys.stdout, ensure_ascii=False, indent=1)
sys.stdout.write("\n")
