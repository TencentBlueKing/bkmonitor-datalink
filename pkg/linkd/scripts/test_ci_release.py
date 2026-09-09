"""CI 编排回归测试：用隔离的假 Docker 验证副作用，不访问本机 Docker。"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('ci-release.sh')
FAKE_DOCKER = r'''#!/usr/bin/env python3
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
    ref = a[a.index('--tag')+1]
    if os.environ.get('FAIL_BUILD') and 'console' in ref: sys.exit(1)
    images[ref] = 'sha256:' + ('b'*64 if 'console' in ref else 'a'*64)
elif a[:2] == ['image', 'inspect']:
    if a[-1] not in images: sys.exit(1)
    print('linux/amd64' if a[3] == '{{.Os}}/{{.Architecture}}' else images[a[-1]])
elif a[:1] == ['run']:
    print('version: ' + os.environ['PACKAGE_VERSION'])
    print('git_commit: ' + ('0'*40 if os.environ.get('BAD_VERSION') else '1'*40))
elif a[:1] == ['tag']: images[a[2]] = images[a[1]]
elif a[:1] == ['push']:
    if os.environ.get('FAIL_PUSH'): sys.exit(1)
elif a[:1] == ['ps']:
    if os.environ.get('IN_USE'): print('container-id')
elif a[:2] == ['image', 'rm']: images.pop(a[-1], None)
else: raise RuntimeError(a)
p.write_text(json.dumps(images))
'''

class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        scripts = self.root / 'module/scripts'
        scripts.mkdir(parents=True)
        self.script = scripts / SCRIPT.name
        shutil.copyfile(SCRIPT, self.script)
        binary = self.root / 'bin'
        binary.mkdir()
        for name, content in [('docker', FAKE_DOCKER), ('git', '#!/bin/sh\nprintf "%s\\n" 1111111111111111111111111111111111111111\n')]:
            file = binary / name
            file.write_text(content)
            file.chmod(0o755)
        self.env = dict(os.environ, PATH=str(binary) + ':' + os.environ['PATH'],
                        FAKE_ROOT=str(self.root), RELEASE_ID='test-123', TARGET_ARCH='amd64',
                        PACKAGE_VERSION='0.1.0-ci.123', IMAGE_REPOSITORY='registry.example/project/images',
                        REGISTRY_USER='test', REGISTRY_PASSWORD='test-token')
        self.state = self.root / 'module/.release/test-123-amd64'

    def run_script(self, command, success=True, **env):
        result = subprocess.run(['bash', str(self.script), command], env=dict(self.env, **env),
                                capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def images(self):
        p = self.root / 'images.json'
        return json.loads(p.read_text()) if p.exists() else {}

    def test_success_and_cleanup_preserves_unrelated_images(self):
        (self.root / 'images.json').write_text(json.dumps({'unrelated:keep': 'sha256:'+'c'*64}))
        self.run_script('build')
        self.run_script('push')
        self.assertEqual(len(self.images()), 5)
        self.run_script('cleanup')
        self.assertEqual(list(self.images()), ['unrelated:keep'])
        self.assertFalse(self.state.exists())

    def test_partial_build_cannot_push_and_is_cleaned(self):
        self.run_script('build', success=False, FAIL_BUILD='1')
        self.run_script('push', success=False)
        self.run_script('cleanup')
        self.assertEqual(self.images(), {})

    def test_invalid_version_and_wrong_architecture_fail_before_build(self):
        for version in ('../bad', '1.2.3;echo bad', '1.2.3-01', '1.2.3+meta', 'v1.2.3'):
            self.run_script('build', success=False, PACKAGE_VERSION=version)
        self.run_script('build', success=False, FAKE_ARCH='linux/arm64')
        self.assertEqual(self.images(), {})
        self.run_script('cleanup')
        self.assertFalse(self.state.exists())

    def test_version_smoke_failure_blocks_push(self):
        self.run_script('build', success=False, BAD_VERSION='1')
        self.run_script('push', success=False)
        self.run_script('cleanup')
        self.assertEqual(self.images(), {})

    def test_failed_push_blocks_chart(self):
        self.run_script('build')
        self.run_script('push', success=False, FAIL_PUSH='1')
        self.run_script('chart', success=False)

    def test_peer_commit_mismatch_blocks_chart(self):
        self.run_script('build')
        self.run_script('push')
        self.run_script('chart', success=False, PEER_REVISION='2'*40, PEER_IMAGE_REPOSITORY='registry.example/project/images-arm')

    def test_cleanup_preserves_in_use_and_replaced_tags(self):
        self.run_script('build')
        self.run_script('cleanup', IN_USE='1')
        self.assertEqual(len(self.images()), 2)
        self.run_script('build')
        images = self.images()
        replaced = next(iter(images))
        images[replaced] = 'sha256:'+'d'*64
        (self.root / 'images.json').write_text(json.dumps(images))
        self.run_script('cleanup')
        self.assertEqual(list(self.images()), [replaced])

if __name__ == '__main__':
    unittest.main()
