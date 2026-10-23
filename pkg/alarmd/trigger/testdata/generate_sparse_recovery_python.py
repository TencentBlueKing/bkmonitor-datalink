#!/usr/bin/env python3
"""Generate a source-executed oracle for Python's recovery check on sparse series.

Usage: python3 generate_sparse_recovery_python.py --python-repo PATH > sparse_recovery_python.json

The source of RecoverStatusChecker.check_result_cache is read from the pinned git
revision of the bk-monitor repository, not from the working tree, and executed
verbatim against an in-memory sorted set that stands in for CHECK_RESULT_CACHE_KEY.
Only the Django, Redis and Alert collaborators are replaced; the window walk, the
bisect counting and the empty-range guard are Python's own code.

The arguments it is called with are derived the way check_recovery derives them
(recover.py:269 and :287): recovery_window_offset = unit * R and
trigger_window_offset = unit * W - 1.

Two readings per case:
- points: check_result_cache with last_check_timestamp at each point's own time,
  the verdict of a check made as the point arrives.
- lifecycle: the alert opened by the first trigger, checked every minute the way
  check_recovery does for a time series (recover.py:297-332), with the default
  "recovery" status setter: last_check_timestamp is the newest reported point,
  floored at now - max(max(W, EVENT_NO_DATA_TOLERANCE_WINDOW_SIZE) * unit,
  30 minutes) (:323-332; the setting is 5, bkmonitor/config/role/worker.py), and
  check_result_cache decides, its empty range (:429-431) included.
- lifecycle_recovery_nodata: the same with a "recovery-nodata" status setter
  (:255-263), under which the alert is closed for no data once now > last +
  recovery_window_offset + trigger_window_offset + window_unit (:315-321).
  The other no-data close, the last-checkpoint key having expired (:309-312),
  depends on a Redis TTL and is not modelled.
These lines are re-encoded here, not executed: they read Redis and Django
settings. Each outcome is the first of RECOVERED or CLOSED_NO_DATA and its
minute, or NEITHER by the end of the case.
"""

import argparse
import ast
import bisect
import hashlib
import json
import logging
import subprocess
import textwrap

REVISION = "00e2fd5c927ddc3c70e06ded6a1b1e405940a660"
PATH = "bkmonitor/alarm_backends/service/alert/manager/checker/recover.py"
UNIT = 60
BASE = 1000 * UNIT
ANOMALY_LABEL = "ANOMALY"


def load_check_result_cache(repo):
    source = subprocess.check_output(["git", "-C", repo, "show", f"{REVISION}:{PATH}"]).decode()
    digest = hashlib.sha256(source.encode()).hexdigest()
    tree = ast.parse(source)
    for node in ast.walk(tree):
        if isinstance(node, ast.FunctionDef) and node.name == "check_result_cache":
            return textwrap.dedent(ast.get_source_segment(source, node)), digest
    raise SystemExit("check_result_cache not found")


class SortedSet:
    def __init__(self, members):
        self.members = members

    def zrangebyscore(self, name, min, max, withscores):
        return sorted(((label, score) for label, score in self.members if min <= score <= max), key=lambda item: item[1])


class CacheKey:
    def __init__(self):
        self.client = None

    def get_key(self, **kwargs):
        return "check-result"


class Parser:
    def __init__(self, event_id):
        self.strategy_id, self.item_id, self.dimensions_md5, self.level = 101, 1, "d", 1


class FakeAlert:
    id = "alert"
    strategy_id = 101
    top_event = {"event_id": "event"}

    def __init__(self):
        self.extra = {}

    def is_recovering(self):
        return self.extra.get("is_recovering", False)

    def update_extra_info(self, key, value):
        self.extra[key] = value

    def get_extra_info(self, key, default=None):
        return self.extra.get(key, default)

    @property
    def extra_info(self):
        return self.extra

    def add_log(self, *args, **kwargs):
        pass


class AlertLog:
    class OpType:
        ABORT_RECOVER = "ABORT_RECOVER"
        RECOVERING = "RECOVERING"


def build(function_source):
    cache_key = CacheKey()
    namespace = {
        "bisect": bisect,
        "logger": logging.getLogger("oracle"),
        "EventIDParser": Parser,
        "CHECK_RESULT_CACHE_KEY": cache_key,
        "ANOMALY_LABEL": ANOMALY_LABEL,
        "AlertLog": AlertLog,
        "Alert": FakeAlert,
        "_": lambda text: text,
    }
    exec(compile(function_source, PATH, "exec"), namespace)
    return namespace["check_result_cache"], cache_key


EVENT_NO_DATA_TOLERANCE_WINDOW_SIZE = 5


def recovery_windows_have_a_point(times, last, window, recovery):
    """Whether each of the R windows check_result_cache walks at last holds a point."""
    end = last
    for _ in range(recovery):
        start = end - (UNIT * window - 1)
        if not any(start <= t <= end for t in times):
            return False
        end -= UNIT
    return True


def lifecycle(check, cache_key, case, opened_at, members, recovery_with_nodata):
    window, required, recovery = case["window"], case["required"], case["recovery"]
    if opened_at is None:
        return {"outcome": "NEITHER"}
    end = members[-1][1] + UNIT * (recovery + window + 2)
    tolerance = max(max(window, EVENT_NO_DATA_TOLERANCE_WINDOW_SIZE) * UNIT, 30 * 60)
    now = opened_at
    while now <= end:
        present = [(label, score) for label, score in members if score <= now]
        last = max(score for _, score in present)
        if recovery_with_nodata and now > last + UNIT * recovery + (UNIT * window - 1) + UNIT:
            return {"outcome": "CLOSED_NO_DATA", "at": now}
        cache_key.client = SortedSet(present)
        last_check = max(last, now - tolerance)
        recovered, _ = check(
            None, alert=FakeAlert(), last_check_timestamp=last_check,
            recovery_window_offset=UNIT * recovery, recovery_window_size=recovery,
            trigger_window_offset=UNIT * window - 1, trigger_count=required,
            window_unit=UNIT, is_time_series=True,
        )
        if recovered:
            times = [score for _, score in present]
            return {"outcome": "RECOVERED", "at": now,
                    "every_window_has_a_point": recovery_windows_have_a_point(times, last_check, window, recovery)}
        now += UNIT
    return {"outcome": "NEITHER"}


def evaluate(check, cache_key, case):
    window, required, recovery = case["window"], case["required"], case["recovery"]
    members = []
    results = []
    opened_at = None
    for minute, anomalous in zip(case["minutes"], case["anomalous"]):
        timestamp = BASE + minute * UNIT
        members.append((f"{timestamp}|{ANOMALY_LABEL}" if anomalous else f"{timestamp}|1", timestamp))
        anomalies = [score for label, score in members if label.endswith(ANOMALY_LABEL)]
        if opened_at is None and anomalous:
            start = timestamp - (UNIT * window - 1)
            if sum(1 for score in anomalies if start <= score <= timestamp) >= required:
                opened_at = timestamp
        cache_key.client = SortedSet(list(members))
        recovered, _ = check(
            None,
            alert=FakeAlert(),
            last_check_timestamp=timestamp,
            recovery_window_offset=UNIT * recovery,
            recovery_window_size=recovery,
            trigger_window_offset=UNIT * window - 1,
            trigger_count=required,
            window_unit=UNIT,
            is_time_series=True,
        )
        results.append({"time": timestamp, "anomalous": anomalous, "recovered": bool(recovered)})
    return (opened_at, results, lifecycle(check, cache_key, case, opened_at, members, False),
            lifecycle(check, cache_key, case, opened_at, members, True))


def cases():
    yield {"id": "exact_42_47_56", "window": 5, "required": 1, "recovery": 5,
           "minutes": [42, 47, 56], "anomalous": [True, False, False]}
    yield {"id": "every_five_minutes", "window": 5, "required": 1, "recovery": 5,
           "minutes": [index * 5 for index in range(14)], "anomalous": [index in (0, 3) for index in range(14)]}
    # Points every five minutes, one anomaly, five recovery windows, under a
    # trigger window of two and of five: the two shapes compared with
    # production Python's recoveries.
    for window in (2, 5):
        yield {"id": f"every_five_minutes_window_{window}", "window": window, "required": 1, "recovery": 5,
               "minutes": [index * 5 for index in range(8)], "anomalous": [index == 1 for index in range(8)]}
        yield {"id": f"every_three_minutes_window_{window}", "window": window, "required": 1, "recovery": 5,
               "minutes": [index * 3 for index in range(12)], "anomalous": [index == 1 for index in range(12)]}
    # The shape of a production series under trigger window 2 and five
    # recovery windows: gaps of 8, 9 and 16 minutes between its points.
    yield {"id": "gaps_8_9_16_window_2", "window": 2, "required": 1, "recovery": 5,
           "minutes": [0, 8, 17, 33], "anomalous": [True, False, False, False]}
    yield {"id": "gaps_16_8_9_window_2", "window": 2, "required": 1, "recovery": 5,
           "minutes": [0, 16, 24, 33], "anomalous": [True, False, False, False]}
    # A production alert's timing and flags, nothing else of it: trigger
    # window 2 needing 2, five recovery windows, anomalous points at :22, :23
    # and :32, a normal one at :33, nothing between. The lone anomaly at :32
    # is under the threshold of two. Python recovered it a minute after :33.
    yield {"id": "production_w2_n2_r5", "window": 2, "required": 2, "recovery": 5,
           "minutes": [22, 23, 32, 33], "anomalous": [True, True, True, False]}
    # Four of five, five recovery windows: anomalies at :13 to :16, then a
    # point every minute, and the same with a point every five minutes after.
    yield {"id": "four_of_five_dense", "window": 5, "required": 4, "recovery": 5,
           "minutes": list(range(13, 31)), "anomalous": [13 <= minute <= 16 for minute in range(13, 31)]}
    yield {"id": "four_of_five_sparse_after", "window": 5, "required": 4, "recovery": 5,
           "minutes": [13, 14, 15, 16, 21, 26, 31, 36], "anomalous": [True, True, True, True, False, False, False, False]}
    # Bursts of one to three minutes with six to thirteen minutes of nothing
    # between them, the shape Python closes for no data.
    bursts = {
        "burst_09_11_then_13_quiet": [(9, True), (10, False), (11, False), (25, False), (26, False)],
        "bursts_gaps_12_13": [(0, True), (1, False), (2, False), (15, False), (16, False), (29, False), (30, False)],
        "bursts_gaps_6_8": [(0, True), (7, False), (8, False), (16, False), (17, False), (24, False)],
        "bursts_mixed": [(0, False), (1, True), (2, False), (10, False), (23, False), (24, False)],
    }
    for name, points in bursts.items():
        yield {"id": name, "window": 5, "required": 1, "recovery": 5,
               "minutes": [minute for minute, _ in points], "anomalous": [anomalous for _, anomalous in points]}
    placements = {"first": [0], "first_two": [0, 1], "third": [2], "run_of_three": [2, 3, 4], "apart": [1, 5]}
    for window, required, recovery in ((5, 1, 5), (5, 2, 3), (3, 1, 2)):
        for every in range(1, 10):
            for name, anomalous in placements.items():
                count = 14
                yield {"id": f"w{window}_n{required}_r{recovery}_every{every}_{name}",
                       "window": window, "required": required, "recovery": recovery,
                       "minutes": [index * every for index in range(count)],
                       "anomalous": [index in anomalous for index in range(count)]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--python-repo", required=True)
    args = parser.parse_args()
    function_source, digest = load_check_result_cache(args.python_repo)
    check, cache_key = build(function_source)
    output = {"revision": REVISION, "path": PATH, "source_sha256": digest, "unit": UNIT, "cases": []}
    for case in cases():
        opened_at, results, life, life_nodata = evaluate(check, cache_key, case)
        output["cases"].append({"id": case["id"], "window": case["window"], "required": case["required"],
                                "recovery": case["recovery"], "opened_at": opened_at, "points": results,
                                "lifecycle": life, "lifecycle_recovery_nodata": life_nodata})
    print(json.dumps(output, indent=1, sort_keys=True))


if __name__ == "__main__":
    main()
