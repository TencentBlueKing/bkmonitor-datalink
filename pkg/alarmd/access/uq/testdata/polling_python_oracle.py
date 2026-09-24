#!/usr/bin/env python3
"""Source-executed, stage-specific polling contracts; not an end-to-end oracle.

Run with --python-repo pointing to a clone containing SOURCE_REVISION. No service
or third-party dependency is needed. AST methods execute verbatim. Each input
describes the method boundary; explicit harness attributes stand for prior stages.
External API calls capture request kwargs, and numeric epoch parsing uses a small
Arrow stand-in. This does not validate authentication, metadata routing, query
functions, ES/BKSQL execution, identity hashing, or serializer acceptance.
"""

import argparse
import ast
import copy
import hashlib
import itertools
import json
import logging
import re
import subprocess
from pathlib import Path
from types import SimpleNamespace as NS

SOURCE_REVISION = "a40283a11e8b9a89a147851939cbbe90958df4e9"
DS = "bkmonitor/bkmonitor/data_source/data_source/__init__.py"
UQ = "bkmonitor/bkmonitor/data_source/unify_query/query.py"
ES = "bkmonitor/bkmonitor/data_source/backends/elastic_search/compiler.py"
FTA = "bkmonitor/bkmonitor/data_source/backends/fta_event/compiler.py"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--python-repo", required=True, type=Path)
    parser.add_argument(
        "--output", type=Path, default=Path(__file__).with_suffix(".json")
    )
    args = parser.parse_args()
    sources = {}
    env = dict(
        copy=copy,
        re=re,
        itertools=itertools,
        chain=itertools.chain,
        product=itertools.product,
        logger=logging.getLogger("oracle"),
        settings=NS(IS_ACCESS_BK_DATA=False),
        AggMethods={},
        CpAggMethods={},
        DataSourceLabel=NS(BK_DATA="bk_data"),
        APM_METRIC_DATA_LABEL="apm_metric",
        AGG_METHOD_REAL_TIME="REAL_TIME",
    )

    def tree(path):
        data = subprocess.check_output(
            ["git", "-C", str(args.python_repo), "show", f"{SOURCE_REVISION}:{path}"]
        )
        sources[path] = hashlib.sha256(data).hexdigest()
        return ast.parse(data, filename=path)

    def execute(nodes, path):
        future = ast.ImportFrom(
            module="__future__", names=[ast.alias(name="annotations")], level=0
        )
        exec(
            compile(
                ast.fix_missing_locations(
                    ast.Module(body=[future, *nodes], type_ignores=[])
                ),
                path,
                "exec",
            ),
            env,
        )

    def function(path, name):
        node = next(
            n
            for n in tree(path).body
            if isinstance(n, ast.FunctionDef) and n.name == name
        )
        node.decorator_list = []
        execute([node], path)
        return env[name]

    def cls(path, original, methods, name=None, base="object", assignments=()):
        node = next(
            n
            for n in tree(path).body
            if isinstance(n, ast.ClassDef) and n.name == original
        )
        body = [
            n
            for n in node.body
            if (isinstance(n, ast.FunctionDef) and n.name in methods)
            or (
                isinstance(n, ast.Assign)
                and any(
                    isinstance(t, ast.Name) and t.id in assignments for t in n.targets
                )
            )
            or (
                isinstance(n, ast.AnnAssign)
                and isinstance(n.target, ast.Name)
                and n.target.id in assignments
            )
        ]
        assert len([n for n in body if isinstance(n, ast.FunctionDef)]) == len(methods)
        node = ast.ClassDef(
            name=name or original,
            bases=[ast.Name(id=base, ctx=ast.Load())],
            keywords=[],
            body=body,
            decorator_list=[],
        )
        execute([node], path)
        return env[name or original]

    function(DS, "_filter_dict_to_conditions")
    function(DS, "_parse_function_params")
    function(
        "bkmonitor/bkmonitor/data_source/unify_query/functions.py",
        "normalize_metric_method",
    )
    ts = cls(DS, "TimeSeriesDataSource", ["to_unify_query_config"])
    log = cls(
        DS,
        "BaseBkMonitorLogDataSource",
        ["to_unify_query_config"],
        assignments=["FUNC_METHOD_MAPPING"],
    )
    prom = cls(
        DS,
        "PrometheusTimeSeriesDataSource",
        ["filter_dict_to_promql_match", "_execute_promql"],
    )
    results = cls(
        UQ,
        "UnifyQuery",
        ["process_unify_query_data", "extract_unify_query_series_dimensions"],
    )
    results.RE_DIMENSION_SUFFIX = re.compile(r"_table\d+$")
    env["arrow"] = NS(get=lambda value: NS(timestamp=float(value)))
    env["time_interval_align"] = lambda value, interval: value // interval * interval
    env["timezone"] = NS(get_current_timezone_name=lambda: "Asia/Shanghai")
    env["api"] = NS(unify_query=NS(query_data_by_promql=lambda **kw: kw))
    cls(
        ES,
        "SQLCompiler",
        ["_get_agg_method_dict"],
        name="ElasticCompiler",
        assignments=["METRIC_AGG_TRANSLATE"],
    )
    fta = cls(
        FTA,
        "SQLCompiler",
        [
            "_get_aggregations",
            "_convert_tag_query",
            "_operate_eq",
            "_operate_neq",
            "_operate_include",
        ],
        base="ElasticCompiler",
        assignments=["RAW_FIELDS", "TAGS_FIELD_PREFIX", "DEFAULT_TIME_FIELD"],
    )

    class InitBoundary:
        def __init__(self, **kwargs):
            self.filter_dict = {}

        def _update_params_by_advance_method(self):
            pass  # Empty where only in the intrinsic-filter cases below.

        def switch_unify_query(self, bk_biz_id):
            return True  # Do not invoke external metadata/space routing.

    env["InitBoundary"] = InitBoundary
    env["EventStatus"] = cls(
        "bkmonitor/constants/alert.py", "EventStatus", [], assignments=["ABNORMAL"]
    )
    event_constants = tree("bkmonitor/constants/event.py")
    execute(
        [
            n
            for n in event_constants.body
            if isinstance(n, ast.Assign)
            and any(
                isinstance(t, ast.Name)
                and t.id in ["ALL_EVENT_PLUGIN_METRIC", "EVENT_PLUGIN_METRIC_PREFIX"]
                for t in n.targets
            )
        ],
        "bkmonitor/constants/event.py",
    )
    env["constants"] = NS(
        event=NS(
            ALL_EVENT_PLUGIN_METRIC=env["ALL_EVENT_PLUGIN_METRIC"],
            EVENT_PLUGIN_METRIC_PREFIX=env["EVENT_PLUGIN_METRIC_PREFIX"],
        )
    )
    fta_source = cls(
        DS,
        "BkFtaEventDataSource",
        ["__init__"],
        base="InitBoundary",
        assignments=["DEFAULT_TIME_FIELD"],
    )
    env["RECOVERY"] = (
        "recovery"  # constants/data_source.py literal; hash provenance below.
    )
    tree("bkmonitor/constants/data_source.py")
    custom_event = cls(DS, "CustomEventDataSource", ["__init__"], base="InitBoundary")
    cases = []

    def add(id, stage, source, input, expected):
        cases.append(
            dict(id=id, stage=stage, source=source, input=input, expected=expected)
        )

    for source in ["bk_monitor", "custom"]:
        for method in ["COUNT", "AVG", "SUM"]:
            attrs = dict(
                data_source_label=source,
                table="Example.Metric",
                data_label="",
                metrics=[dict(field="value", method=method, alias="A")],
                filter_dict={"host__eq": ["node-a", "node-b"]},
                where=[],
                group_by=["host"],
                time_field="time",
                time_offset=0,
                time_shift="",
                interval=60,
            )
            obj = ts()
            obj.__dict__.update(copy.deepcopy(attrs))
            obj._parse_function_params = lambda: ({}, [])
            add(
                f"{source}-{method.lower()}",
                "uq_query_config",
                f"{DS}:TimeSeriesDataSource.to_unify_query_config",
                attrs,
                obj.to_unify_query_config(),
            )

    # At this boundary field/table/condition expansion has already happened. The
    # fixtures explicitly expose that input instead of claiming to test expansion.
    for source, kind, datasource, table in [
        ("bk_log_search", "log", "bklog", "bklog_index_set_7"),
        ("bk_log_search", "time_series", "bklog", "bklog_index_set_7"),
        ("bk_monitor", "log", "bkapm", "events.__default__"),
        ("custom", "event", "bkapm", "custom_events.__default__"),
    ]:
        for aligned in [True, False]:
            attrs = dict(
                source_label=source,
                type_label=kind,
                data_source=datasource,
                table_id=table,
                group_by=["host"],
                time_field="time",
                query_string="error",
                conditions={"field_list": [], "condition_list": []},
                metrics=[dict(field="_index", method="COUNT", alias="A")],
                interval=60,
                time_alignment=aligned,
                is_time_agg=True,
                functions=[],
            )
            obj = log()
            obj.__dict__.update(copy.deepcopy(attrs))
            obj._get_group_by = lambda: attrs["group_by"]
            obj._get_datasource = lambda: attrs["data_source"]
            obj._get_unify_query_table = lambda: attrs["table_id"]
            obj._get_unify_query_string = lambda: attrs["query_string"]
            obj._get_conditions = lambda: attrs["conditions"]
            add(
                f"{source}-{kind}-aligned-{aligned}",
                "uq_query_config",
                f"{DS}:BaseBkMonitorLogDataSource.to_unify_query_config",
                attrs,
                obj.to_unify_query_config(),
            )

    attrs = dict(
        bk_biz_id=2,
        promql="sum(rate(requests_total[5m]))",
        interval=60,
        filter_dict={
            "host": "node-a",
            "nested": {"zone": "east"},
            "ignored_list": ["x"],
        },
    )
    obj = prom()
    obj.__dict__.update(attrs)
    request, aligned_end = obj._execute_promql(1725000011000, 1725000123000)
    add(
        "promql-request",
        "promql_api_request",
        f"{DS}:PrometheusTimeSeriesDataSource._execute_promql",
        dict(**attrs, start_ms=1725000011000, end_ms=1725000123000),
        dict(request=request, aligned_end_ms=aligned_end),
    )

    for instant in [False, True]:
        raw = dict(
            series=[
                dict(
                    group_keys=["host_table0", "zone"],
                    group_values=["node-a"],
                    columns=["_time", "_value"],
                    types=["time", "double"],
                    values=[
                        [1725000000, 0],
                        [1725000060, None],
                        [1725000120, 1.23456789],
                    ],
                )
            ]
        )
        params = dict(instant=instant)
        add(
            f"uq-result-instant-{instant}",
            "uq_result_records",
            f"{UQ}:UnifyQuery.process_unify_query_data",
            dict(params=params, response=raw, end_time_ms=1725000120000),
            results.process_unify_query_data(params, copy.deepcopy(raw), 1725000120000),
        )

    for field, value in [
        ("tags.service", ["api"]),
        ("alert_name.raw", ["latency"]),
        ("time", {"gte": 1725000000123, "lt": 1725000060999}),
    ]:
        op = "range" if field == "time" else "terms"
        add(
            f"fta-filter-{field}",
            "fta_filter_dsl",
            f"{FTA}:SQLCompiler._convert_tag_query",
            dict(operate=op, field=field, value=value),
            fta._convert_tag_query(op, field, copy.deepcopy(value)),
        )
    for dimensions in [["tags.service"], ["bk_biz_id", "tags.service", "alert_name"]]:
        obj = fta()
        obj.query = NS(time_field="time")
        fields = [dict(agg_method="count", metric_field="_index", metric_alias="a")]
        add(
            "fta-aggregation-" + str(len(dimensions)),
            "fta_aggregation_dsl",
            f"{FTA}:SQLCompiler._get_aggregations",
            dict(interval_minutes=1, dimensions=dimensions, select_fields=fields),
            obj._get_aggregations(1, dimensions, fields),
        )

    for alert_name in ["latency", "__ALL_EVENT_PLUGIN__", "__EVENT_PLUGIN__example"]:
        attrs = dict(
            metrics=[dict(field=alert_name, method="COUNT", alias="a")],
            interval=120,
            group_by=["tags.service"],
            where=[],
            bk_biz_id=2,
        )
        obj = fta_source(**copy.deepcopy(attrs))
        add(
            "fta-intrinsic-" + alert_name,
            "source_intrinsic_filters",
            f"{DS}:BkFtaEventDataSource.__init__",
            attrs,
            dict(
                metrics=obj.metrics,
                interval_minutes=obj.interval,
                filter_dict=obj.filter_dict,
                group_by=obj.group_by,
            ),
        )
    for name in ["", "deploy"]:
        obj = custom_event(custom_event_name=name, bk_biz_id=2)
        add(
            "custom-event-intrinsic-" + (name or "all"),
            "source_intrinsic_filters",
            f"{DS}:CustomEventDataSource.__init__",
            dict(custom_event_name=name, metadata_route="uq"),
            obj.filter_dict,
        )

    function("bkmonitor/bkmonitor/utils/common_utils.py", "number_format")
    record_class = cls(
        "bkmonitor/alarm_backends/service/access/data/records.py",
        "DataRecord",
        ["_convert"],
    )
    env["settings"].POINT_PRECISION = 6
    numeric_values = [
        0,
        1.23456789,
        -1.23456789,
        "1.23456789",
        "0",
        "42",
        0.0000005,
        0.0000015,
    ]
    add(
        "data-record-numeric-rounding",
        "data_record_numeric_conversion",
        "bkmonitor/alarm_backends/service/access/data/records.py:DataRecord._convert",
        dict(values=numeric_values, precision=6),
        [record_class()._convert(value) for value in numeric_values],
    )
    payload = dict(
        schema_version=1,
        source_revision=SOURCE_REVISION,
        source_sha256=sources,
        limitations=[
            "Stage-specific fixtures, not full strategy-to-wire proof",
            "No source initialization, metadata, routing, auth or SQL/ES transport execution",
            "No configured query functions or percentile aggregations",
            "Numeric epoch only; timestamp parser is a harness stand-in",
        ],
        cases=cases,
    )
    args.output.write_text(
        json.dumps(payload, sort_keys=True, indent=2, ensure_ascii=False) + "\n"
    )
    print(f"generated {len(cases)} source-executed cases")


if __name__ == "__main__":
    main()
