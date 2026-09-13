#!/usr/bin/env python3
"""Generate a source-executed oracle, without Django/Redis or third-party packages.

Usage: python3 generate_remaining_algorithms_python.py --python-repo PATH
Source is read from the pinned git revision, not the working tree. AST extraction
removes imports, serializers and message rendering only: gen_expr, extra_context,
history offsets/fetchers, short-circuit detection and unit conversion run verbatim.
Inputs are already validated configs. This is not a serializer or message oracle.

raw supplies exact cache points, including zero. history_loader executes the real
HistoryPointFetcher.query_history_points with an in-memory query/publisher; its
`if point.value` removes zero. LOG/EVENT then call the real set_default(0), as the
DetectMixin does. Query failures are separate from missing successful-query points.
UNAVAILABLE means Python detection raised (production catches and skips the point),
not a normal detection result. PARTIAL is tested with a cold empty history cache.
"""

import argparse
import ast
import functools
import hashlib
import inspect
import json
import logging
from pathlib import Path
import subprocess
from types import SimpleNamespace
from typing import Optional, Tuple, Union


REVISION = "a40283a11e8b9a89a147851939cbbe90958df4e9"
ROOT = "bkmonitor/alarm_backends/service/detect/strategy/"
DAY = 86400
NOW = 10 * DAY


class OracleError(Exception):
    def __init__(self, **kwargs):
        super().__init__(str(kwargs))


class Record:
    def __init__(self, item, record):
        self.data = record
        self.value = record["value"]

    def clean(self):
        pass


def load_source(repo):
    hashes = {}

    def read(path):
        data = subprocess.check_output(
            ["git", "-C", str(repo), "show", f"{REVISION}:{path}"]
        )
        hashes[path] = hashlib.sha256(data).hexdigest()
        return data.decode()

    env = dict(
        functools=functools,
        inspect=inspect,
        Optional=Optional,
        Tuple=Tuple,
        Union=Union,
        settings=SimpleNamespace(POINT_PRECISION=6),
        logger=logging.getLogger("oracle"),
        CONST_ONE_DAY=DAY,
        CONST_ONE_WEEK=7 * DAY,
        safe_int=int,
        mark_safe=lambda s: s,
        _=lambda s: s,
        HistoryDataNotExists=OracleError,
        InvalidDataPoint=OracleError,
        InvalidAlgorithmsConfig=OracleError,
        DataRecord=Record,
        six=SimpleNamespace(iteritems=lambda d: d.items()),
        YEAR_ROUND_ALLOWED_METHODS={
            "gt": ">",
            "gte": ">=",
            "lt": "<",
            "lte": "<=",
            "eq": "==",
        },
        AnomalyDataPoint=lambda **kw: SimpleNamespace(**kw),
        unit_auto_convert=lambda *args: "",
    )

    def extract(path, names):
        tree = ast.parse(read(path), filename=path)
        selected = [
            n
            for n in tree.body
            if isinstance(n, (ast.ClassDef, ast.FunctionDef)) and n.name in names
        ]
        assert {n.name for n in selected} == set(names), (path, names)
        for node in selected:
            node.decorator_list = []
            if isinstance(node, ast.ClassDef):
                for child in node.body:
                    if isinstance(child, ast.Assign) and any(
                        isinstance(t, ast.Name) and t.id == "config_serializer"
                        for t in child.targets
                    ):
                        child.value = ast.Constant(None)
        module = ast.fix_missing_locations(ast.Module(body=selected, type_ignores=[]))
        exec(compile(module, path, "exec"), env)

    extract(
        "bkmonitor/core/unit/models.py",
        ["ScaledUnits", "Percent", "BinarySIPrefix", "TimeUnit"],
    )
    # The registry mapping is copied from core/unit/init_data.py; actual conversion
    # classes and unit_convert_min are executed from source. Hash the mapping source.
    read("bkmonitor/core/unit/init_data.py")
    units = {
        "": env["ScaledUnits"](),
        "short": env["ScaledUnits"](),
        "bytes": env["BinarySIPrefix"]("B"),
        "kbytes": env["BinarySIPrefix"]("B", 1),
        "percent": env["Percent"](0),
        "percentunit": env["Percent"](1),
        "ms": env["TimeUnit"](suffix_idx=2),
    }
    # Unknown unit ids are fixed units in load_unit (including the prefix "Ki"
    # passed as an id by RingRatioAmplitude's first threshold expression).
    env["load_unit"] = lambda name: units.get(name, units[""])
    extract("bkmonitor/bkmonitor/utils/common_utils.py", ["safe_int"])
    extract("bkmonitor/alarm_backends/templatetags/unit.py", ["unit_convert_min"])
    extract("bkmonitor/alarm_backends/service/detect/core.py", ["DataPoint"])
    extract(
        ROOT + "__init__.py",
        [
            "DetectContext",
            "Algorithms",
            "ExprDetectAlgorithms",
            "BasicAlgorithmsCollection",
            "HistoryPointFetcher",
            "RangeRatioAlgorithmsCollection",
            "adapter_data_access_2_detect",
        ],
    )
    env["Algorithms"]._format_message = lambda self, point: ""
    for filename, name in [
        ("simple_ring_ratio", "SimpleRingRatio"),
        ("simple_year_round", "SimpleYearRound"),
        ("advanced_year_round", "AdvancedYearRound"),
        ("advanced_ring_ratio", "AdvancedRingRatio"),
        ("ring_ratio_amplitude", "RingRatioAmplitude"),
        ("year_round_amplitude", "YearRoundAmplitude"),
        ("year_round_range", "YearRoundRange"),
    ]:
        extract(ROOT + filename + ".py", [name])
    return env, hashes


def evaluate(case, env):
    env["settings"].POINT_PRECISION = case["precision"]
    item = SimpleNamespace(
        unit=case["data_unit"],
        data_sources=[None],
        id=1,
        strategy=SimpleNamespace(id=1),
        query_configs=[{"agg_interval": case["aggregation_interval"]}],
        query=SimpleNamespace(is_partial=case["query_state"] == "PARTIAL"),
    )

    def point(offset, value):
        return env["DataPoint"](
            {
                "value": value,
                "time": NOW - int(offset),
                "record_id": f"series.{NOW - int(offset)}",
            },
            item,
        )

    current = point(0, case["current"])
    detector = env[case["kind"]](case["config"], case["algorithm_unit"])
    history = {int(k): point(k, v) for k, v in case["history"].items() if v is not None}
    stored = history if case["history_mode"] == "raw" else {}

    def fetch(_item, _point, timestamp):
        result = stored.get(NOW - timestamp)
        if result is None and getattr(detector, "_default", None) is not None:
            return point(NOW - timestamp, detector._default)
        return result

    detector.fetch_history_point = fetch
    try:
        if case["history_mode"] == "history_loader":

            def query(start, end):
                if case["query_state"] == "UNAVAILABLE":
                    raise ConnectionError("synthetic query failure")
                return [
                    p.as_dict() for p in history.values() if start <= p.timestamp < end
                ]

            item.query_record = query
            detector._check_history_points = lambda _item, timestamp: False
            detector._publish_history_points = lambda _item, points: stored.update(
                {NOW - p.timestamp: p for p in points}
            )
            detector.query_history_points([current])
        if case["source_type"] in ("log", "event"):
            detector.set_default(0)
        result = detector.detect(current)
        return {"status": "ANOMALOUS" if result else "NORMAL"}
    except (
        OracleError,
        AttributeError,
        KeyError,
        NameError,
        IndexError,
        ConnectionError,
    ) as exc:
        return {"status": "UNAVAILABLE", "reason": type(exc).__name__}


def cases():
    result = []

    def add(id, kind, config, current, history, **extra):
        case = dict(
            id=id,
            kind=kind,
            config=config,
            current=current,
            history={str(k): v for k, v in history.items()},
            aggregation_interval=60,
            data_unit="short",
            algorithm_unit="",
            precision=6,
            source_type="time_series",
            history_mode="raw",
            query_state="FULL",
        )
        case.update(extra)
        result.append(case)

    for current in [-120, -100, 0, 80, 100, 120, 120.000001]:
        add(
            f"simple-week-{current}",
            "SimpleYearRound",
            dict(floor=20, ceil=20),
            current,
            {7 * DAY: 100},
        )
    for value in [-100, 0, None]:
        add(
            f"simple-history-{value}",
            "SimpleYearRound",
            dict(floor=20, ceil=20),
            0,
            {7 * DAY: value},
        )

    for kind, step in [("AdvancedRingRatio", 60), ("AdvancedYearRound", DAY)]:
        for fetch in ["avg", "last"]:
            config = dict(
                floor=20, floor_interval=2, ceil=20, ceil_interval=3, fetch_type=fetch
            )
            for current in [-100, 0, 48, 100, 120, 200]:
                add(
                    f"{kind}-{fetch}-{current}",
                    kind,
                    config,
                    current,
                    {step: 100, 2 * step: -20, 3 * step: 200},
                )
            add(
                f"{kind}-{fetch}-missing-middle",
                kind,
                config,
                120,
                {step: 100, 2 * step: None, 3 * step: 200},
            )
            add(f"{kind}-{fetch}-all-missing", kind, config, 120, {})
        config = dict(
            floor=None, floor_interval=None, ceil=20, ceil_interval=2, fetch_type="avg"
        )
        add(
            f"{kind}-rounded-avg",
            kind,
            config,
            0.148148,
            {step: 0.1234564, 2 * step: 0.1234574},
            data_unit="percent",
            precision=6,
        )

    config = dict(ratio=0.5, shock=5, threshold=10)
    for current, previous in [
        (20, 10),
        (10, 20),
        (9, 20),
        (-20, -10),
        (0, 0),
        (25, 10),
        (24.999999, 10),
        (100, None),
    ]:
        add(
            f"ring-amplitude-{current}-{previous}",
            "RingRatioAmplitude",
            config,
            current,
            {60: previous},
        )
    add(
        "ring-amplitude-binary-unit",
        "RingRatioAmplitude",
        dict(ratio=0.5, shock=1, threshold=1),
        2560,
        {60: 1024},
        data_unit="bytes",
        algorithm_unit="Ki",
    )
    add(
        "ring-amplitude-prefixed-data-unit",
        "RingRatioAmplitude",
        dict(ratio=0.5, shock=1, threshold=1),
        2.5,
        {60: 1},
        data_unit="kbytes",
        algorithm_unit="Ki",
    )
    add(
        "ring-amplitude-first-threshold-unknown-prefix",
        "RingRatioAmplitude",
        dict(ratio=0, shock=0, threshold=1),
        512,
        {60: 1024},
        data_unit="bytes",
        algorithm_unit="Ki",
    )

    for method in ["gt", "gte", "lt", "lte", "eq"]:
        config = dict(ratio=2, shock=5, days=2, method=method)
        for current in [-25, 0, 25, 25.000001]:
            add(
                f"range-{method}-{current}",
                "YearRoundRange",
                config,
                current,
                {DAY: -10, 2 * DAY: 30},
            )
            add(
                f"amplitude-{method}-{current}",
                "YearRoundAmplitude",
                config,
                current,
                {0: current, 60: 0, DAY: 10, DAY + 60: 0, 2 * DAY: 30, 2 * DAY + 60: 5},
            )
    for kind in ["YearRoundRange", "YearRoundAmplitude"]:
        config = dict(ratio=1, shock=0, days=2, method="gt")
        base = (
            {DAY: 10, 2 * DAY: None}
            if kind == "YearRoundRange"
            else {0: 20, 60: 0, DAY: 10, DAY + 60: 0, 2 * DAY: None, 2 * DAY + 60: None}
        )
        add(f"{kind}-missing-later-early-hit", kind, config, 20, base)
        base = (
            {DAY: 30, 2 * DAY: None}
            if kind == "YearRoundRange"
            else {0: 20, 60: 0, DAY: 30, DAY + 60: 0, 2 * DAY: None, 2 * DAY + 60: None}
        )
        add(f"{kind}-missing-later-no-hit", kind, config, 20, base)
        base = (
            {DAY: None, 2 * DAY: 10}
            if kind == "YearRoundRange"
            else {0: 20, 60: 0, DAY: None, DAY + 60: None, 2 * DAY: 10, 2 * DAY + 60: 0}
        )
        add(f"{kind}-missing-first", kind, config, 20, base)
        base = (
            {DAY: 1, 2 * DAY: 2}
            if kind == "YearRoundRange"
            else {0: 2, 60: 1, DAY: 1, DAY + 60: 0.5, 2 * DAY: 2, 2 * DAY + 60: 1}
        )
        add(
            f"{kind}-binary-shock",
            kind,
            dict(ratio=1, shock=0.5, days=2, method="gte"),
            2,
            base,
            data_unit="kbytes",
            algorithm_unit="Ki",
        )

    # Explicitly execute the shared loader and DetectMixin's LOG/EVENT default.
    for source in ["time_series", "log", "event"]:
        for value in [0, None]:
            add(
                f"loader-{source}-{value}",
                "SimpleYearRound",
                dict(floor=None, ceil=20),
                1,
                {7 * DAY: value},
                source_type=source,
                history_mode="history_loader",
            )
    for state in ["PARTIAL", "UNAVAILABLE"]:
        add(
            f"query-{state}",
            "SimpleYearRound",
            dict(floor=None, ceil=20),
            120,
            {7 * DAY: 100},
            history_mode="history_loader",
            query_state=state,
        )
    add(
        "loader-amplitude-zero-pair",
        "YearRoundAmplitude",
        dict(ratio=1, shock=0, days=1, method="gt"),
        20,
        {0: 20, 60: 0, DAY: 10, DAY + 60: 0},
        history_mode="history_loader",
    )
    add(
        "loader-amplitude-zero-pair-event",
        "YearRoundAmplitude",
        dict(ratio=1, shock=0, days=1, method="gt"),
        20,
        {0: 20, 60: 0, DAY: 10, DAY + 60: 0},
        history_mode="history_loader",
        source_type="event",
    )
    for precision in [2, 6]:
        add(
            f"range-percent-round-{precision}",
            "YearRoundRange",
            dict(ratio=1, shock=0, days=1, method="eq"),
            1.004,
            {DAY: 1.003},
            data_unit="percent",
            precision=precision,
        )
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--python-repo", type=Path, required=True)
    parser.add_argument(
        "--output",
        type=Path,
        default=Path(__file__).with_name("remaining_algorithms_python.json"),
    )
    args = parser.parse_args()
    env, hashes = load_source(args.python_repo)
    records = cases()
    for case in records:
        case["expected"] = evaluate(case, env)
    payload = dict(
        schema_version=1, source_revision=REVISION, source_sha256=hashes, cases=records
    )
    args.output.write_text(
        json.dumps(payload, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    )
    print(f"generated {len(records)} cases")


if __name__ == "__main__":
    main()
