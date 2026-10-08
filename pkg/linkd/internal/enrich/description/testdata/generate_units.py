"""从指定 bk-monitor 源码执行单位转换，生成跨语言黄金样本。

仅供测试维护，生产运行不依赖 Python。传入 bkmonitor/core/unit 目录；
使用源码快照 51c834dc6，模块只加载 models/init_data，不启动 Django。
"""

import importlib.util
import json
import sys
import types
from pathlib import Path

base = Path(sys.argv[1]).resolve()
package = types.ModuleType("unit_evidence")
package.__path__ = [str(base)]
sys.modules[package.__name__] = package
for name in ["models", "init_data"]:
    spec = importlib.util.spec_from_file_location("unit_evidence." + name, base / (name + ".py"))
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)

samples = []
raw_values = ["0", "0.0", "-0.0", "1", "1.0", "0.9", "-1", "999", "1000", "1024", "1025", "2.675", "0.0000015", "1e-7", "-1e-7", "60", "3600", "24", "-1000", "1e16"]
for category in sys.modules["unit_evidence.init_data"].define:
    for form in category["formats"]:
        fn = form["fn"]
        for raw in raw_values:
            value = json.loads(raw)
            converted, suffix = fn.auto_convert(value, decimal=6)
            minimum, _ = fn.convert_to_max(value, decimal=6)
            samples.append({"unit": form["id"], "category_unit": category["name"] + "||" + form["id"], "raw": raw, "formatted": str(converted) + suffix, "minimum": minimum})
Path(__file__).with_name("units.json").write_text(json.dumps(samples, ensure_ascii=False, indent=2) + "\n")
print("generated", len(samples), "unit golden samples")
