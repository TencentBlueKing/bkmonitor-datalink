# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""读取 KAC 原字段声明和动态模板，比较 Linkd 的静态 mapping；不启动 Django。"""
import ast
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
source = root / "src/kingeye/base/domains/alarm/models/alarm_event_document.py"
classes = [n for n in ast.parse(source.read_text()).body if isinstance(n, ast.ClassDef)]
model = next(n for n in classes if n.name == "AlarmEventDocument")
field_settings = next(ast.literal_eval(n.value) for n in model.body if isinstance(n, ast.Assign) and isinstance(n.targets[0], ast.Name) and n.targets[0].id == "fields_settings")
subclass_source = root / "src/kingeye/kac/alarm/models.py"
subclass = next(n for n in ast.parse(subclass_source.read_text()).body if isinstance(n, ast.ClassDef) and n.name == "AlarmEvent")
properties = {}
fields = {"Keyword": "keyword", "Text": "text", "CWInteger": "integer", "Integer": "integer", "Object": "object", "Date": "date"}
for node in [*model.body, *subclass.body]:
    if not isinstance(node, ast.Assign) or not isinstance(node.value, ast.Call):
        continue
    if not isinstance(node.value.func, ast.Name) or node.value.func.id not in fields:
        continue
    kind = node.value.func.id
    value = {"type": fields[kind]}
    kwargs = {item.arg: item.value for item in node.value.keywords}
    if kind == "Text":
        assert isinstance(kwargs["analyzer"], ast.Name) and kwargs["analyzer"].id == "split_by_whitespace_analyzer"
        value.update(analyzer="split_by_whitespace_analyzer", fields=field_settings, norms=ast.literal_eval(kwargs["norms"]))
    if kind == "Date":
        value["format"] = ast.literal_eval(kwargs["format"])
    properties[node.targets[0].id] = value
meta = next(n for n in subclass.body if isinstance(n, ast.ClassDef) and n.name == "Meta")
values = {n.targets[0].id: ast.literal_eval(n.value.args[0]) for n in meta.body if isinstance(n, ast.Assign)}
assert values["dynamic"] == "true"
print(json.dumps({"dynamic": True, "dynamic_templates": values["dynamic_templates"], "properties": properties}))
