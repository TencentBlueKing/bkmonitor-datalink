"""CI 编排回归测试：用隔离的假 Docker 验证副作用，不访问本机 Docker。

照 pkg/linkd/scripts/test_ci_release.py 的做法：把脚本连同 VERSION/SCHEMA_VERSION 复制进临时模块目录，
PATH 里放假的 docker 与 git，断言脚本的控制流与副作用。
"""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("ci-release.sh")
MODULE = Path(__file__).resolve().parent.parent
REVISION = "1" * 40
FAKE_DOCKER = r"""#!/usr/bin/env python3
import json, os, pathlib, sys
root = pathlib.Path(os.environ['FAKE_ROOT'])
p = root / 'images.json'
images = json.loads(p.read_text()) if p.exists() else {}
a = sys.argv[1:]
with (root / 'calls').open('a') as f: f.write(json.dumps(a) + '\n')
if a[:2] == ['buildx', 'version']: print('buildx fake')
elif a[:1] == ['info']: print(os.environ.get('FAKE_ARCH', 'linux/amd64'))
elif a[:1] == ['login']: sys.stdin.read()
elif a[:2] == ['buildx', 'build']:
    if os.environ.get('FAIL_BUILD'): sys.exit(1)
    ref = a[a.index('--tag')+1]
    images[ref] = 'sha256:' + 'a'*64
elif a[:2] == ['image', 'inspect']:
    if a[-1] not in images: sys.exit(1)
    print('linux/amd64' if a[3] == '{{.Os}}/{{.Architecture}}' else images[a[-1]])
elif a[:1] == ['run']:
    commit = '0'*40 if os.environ.get('BAD_VERSION') else '1'*40
    print(f"alarmd version={os.environ['FAKE_EXPECTED_VERSION']} commit={commit} schema_version={os.environ['FAKE_SCHEMA']}")
elif a[:1] == ['tag']: images[a[2]] = images[a[1]]
elif a[:1] == ['push']:
    if os.environ.get('FAIL_PUSH'): sys.exit(1)
elif a[:1] == ['ps']:
    if os.environ.get('IN_USE'): print('container-id')
elif a[:2] == ['image', 'rm']: images.pop(a[-1], None)
else: raise RuntimeError(a)
p.write_text(json.dumps(images))
"""
# rev-parse HEAD 固定返回 REVISION；tag 只认 FAKE_TAGS（JSON：tag 名 -> 提交）里登记过的。
FAKE_GIT = r"""#!/usr/bin/env python3
import json, os, sys
a = sys.argv[1:]
while a and a[0] in ('-C',): a = a[2:]
if a == ['rev-parse', 'HEAD']: print('1' * 40)
elif a[:3] == ['rev-parse', '--verify', '--quiet'] and a[3].startswith('refs/tags/') and a[3].endswith('^{commit}'):
    tags = json.loads(os.environ.get('FAKE_TAGS', '{}'))
    name = a[3][len('refs/tags/'):-len('^{commit}')]
    if name not in tags: sys.exit(1)
    print(tags[name])
else: raise RuntimeError(a)
"""


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        module = self.root / "module"
        scripts = module / "scripts"
        scripts.mkdir(parents=True)
        self.script = scripts / SCRIPT.name
        shutil.copyfile(SCRIPT, self.script)
        # 版本线与 schema 从真实文件抄，脚本对着它们校验。
        (module / "VERSION").write_text((MODULE / "VERSION").read_text())
        (module / "SCHEMA_VERSION").write_text((MODULE / "SCHEMA_VERSION").read_text())
        self.version_line = (MODULE / "VERSION").read_text().strip()
        major_minor = self.version_line[: self.version_line.rindex(".")]
        binary = self.root / "bin"
        binary.mkdir()
        for name, content in [("docker", FAKE_DOCKER), ("git", FAKE_GIT)]:
            file = binary / name
            file.write_text(content)
            file.chmod(0o755)
        self.major_minor = major_minor
        self.env = dict(
            os.environ,
            PATH=str(binary) + ":" + os.environ["PATH"],
            FAKE_ROOT=str(self.root),
            FAKE_SCHEMA=(MODULE / "SCHEMA_VERSION").read_text().strip(),
            RELEASE_ID="test-123",
            TARGET_ARCH="amd64",
            PACKAGE_VERSION=f"{major_minor}.0-ci.123",
            # 假镜像 --version 报的版本：默认与 PACKAGE_VERSION 相同，按 tag 发布的用例单独给
            FAKE_EXPECTED_VERSION=f"{major_minor}.0-ci.123",
            IMAGE_REPOSITORY="registry.example/project/images",
            REGISTRY_USER="test",
            REGISTRY_PASSWORD="test-token",
        )
        self.state = module / ".release/test-123-amd64"

    def run_script(self, command, success=True, **env):
        result = subprocess.run(
            ["bash", str(self.script), command], env=dict(self.env, **env), capture_output=True, text=True
        )
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def images(self):
        p = self.root / "images.json"
        return json.loads(p.read_text()) if p.exists() else {}

    def test_success_and_cleanup_preserves_unrelated_images(self):
        (self.root / "images.json").write_text(json.dumps({"unrelated:keep": "sha256:" + "c" * 64}))
        result = self.run_script("build")
        self.assertIn(f"::set-output name=revision::{REVISION}", result.stdout)
        self.run_script("push")
        images = self.images()
        # 本地构建标签 + 发布标签 + 无关镜像
        self.assertEqual(len(images), 3)
        self.assertIn(f"registry.example/project/images/alarmd:{self.env['PACKAGE_VERSION']}", images)
        self.run_script("cleanup")
        self.assertEqual(list(self.images()), ["unrelated:keep"])
        self.assertFalse(self.state.exists())

    def test_build_verifies_the_binary_reports_the_build(self):
        self.run_script("build")
        calls = [json.loads(line) for line in (self.root / "calls").read_text().splitlines()]
        runs = [c for c in calls if c[:1] == ["run"]]
        self.assertEqual(len(runs), 1)
        self.assertEqual(runs[0][-1], "--version")

    def test_source_tag_is_the_only_version_coordinate(self):
        # 按 tag 发布：只给 SOURCE_TAG，镜像 tag 就是去掉 pkg/alarmd/v 的那段，HEAD 必须是 tag 指向的提交
        tag = f"pkg/alarmd/v{self.major_minor}.4503"
        env = dict(SOURCE_TAG=tag, PACKAGE_VERSION="", FAKE_EXPECTED_VERSION=f"{self.major_minor}.4503")
        self.run_script("build", FAKE_TAGS=json.dumps({tag: REVISION}), **env)
        self.run_script("push", FAKE_TAGS=json.dumps({tag: REVISION}), **env)
        self.assertIn(f"registry.example/project/images/alarmd:{self.major_minor}.4503", self.images())

    def test_source_tag_that_disagrees_with_the_checkout_is_refused(self):
        tag = f"pkg/alarmd/v{self.major_minor}.4503"
        cases = [
            # tag 指向别的提交
            dict(SOURCE_TAG=tag, PACKAGE_VERSION="", FAKE_TAGS=json.dumps({tag: "2" * 40})),
            # 本地仓根本没有这个 tag：检出步骤没带上它
            dict(SOURCE_TAG=tag, PACKAGE_VERSION="", FAKE_TAGS="{}"),
            # 另填了一个不同的 PACKAGE_VERSION：两个版本坐标
            dict(SOURCE_TAG=tag, PACKAGE_VERSION=f"{self.major_minor}.4504", FAKE_TAGS=json.dumps({tag: REVISION})),
            # 不是本模块的 tag 形式
            dict(SOURCE_TAG=f"v{self.major_minor}.4503", PACKAGE_VERSION=""),
            dict(SOURCE_TAG=f"pkg/linkd/v{self.major_minor}.4503", PACKAGE_VERSION=""),
            dict(SOURCE_TAG="pkg/alarmd/v", PACKAGE_VERSION=""),
        ]
        for case in cases:
            result = self.run_script("build", success=False, FAKE_EXPECTED_VERSION=f"{self.major_minor}.4503", **case)
            self.assertIn("ERROR", result.stderr, case)
        self.assertEqual(self.images(), {})

    def test_partial_build_cannot_push_and_is_cleaned(self):
        self.run_script("build", success=False, FAIL_BUILD="1")
        self.run_script("push", success=False)
        self.run_script("cleanup")
        self.assertEqual(self.images(), {})

    def test_invalid_version_and_wrong_architecture_fail_before_build(self):
        for version in ("../bad", "1.2.3;echo bad", "1.2.3-01", "1.2.3+meta", "v1.2.3"):
            self.run_script("build", success=False, PACKAGE_VERSION=version)
        self.run_script("build", success=False, FAKE_ARCH="linux/arm64")
        self.assertEqual(self.images(), {})
        self.run_script("cleanup")
        self.assertFalse(self.state.exists())

    def test_version_off_the_repository_line_is_refused(self):
        # VERSION 说 0.2.x，就不能打 0.1.* 或 9.9.* 的镜像：镜像 --version 报的版本线必须和代码一致
        result = self.run_script("build", success=False, PACKAGE_VERSION="9.9.0-ci.1")
        self.assertIn(self.version_line, result.stderr)
        self.assertEqual(self.images(), {})

    def test_version_smoke_failure_blocks_push(self):
        self.run_script("build", success=False, BAD_VERSION="1")
        self.run_script("push", success=False)
        self.run_script("cleanup")
        self.assertEqual(self.images(), {})

    def test_failed_push_leaves_no_pushed_marker(self):
        self.run_script("build")
        self.run_script("push", success=False, FAIL_PUSH="1")
        self.assertFalse((self.state / "pushed").exists())

    def test_chart_is_not_a_command_here(self):
        result = self.run_script("chart", success=False)
        self.assertIn("kingeye", result.stderr)

    def test_cleanup_preserves_in_use_and_replaced_tags(self):
        self.run_script("build")
        self.run_script("cleanup", IN_USE="1")
        self.assertEqual(len(self.images()), 1)
        self.run_script("build")
        images = self.images()
        replaced = next(iter(images))
        images[replaced] = "sha256:" + "d" * 64
        (self.root / "images.json").write_text(json.dumps(images))
        self.run_script("cleanup")
        self.assertEqual(list(self.images()), [replaced])


if __name__ == "__main__":
    unittest.main()
