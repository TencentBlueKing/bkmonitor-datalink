const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync, spawnSync } = require('node:child_process');
const { test } = require('node:test');
const YAML = require('yaml');
const source = path.resolve(__dirname, '../deploy/helm/linkd');

// 使用真实 Helm 打包和渲染；拦截网络上传，禁止 Chart 路径调用 Docker 或 Git。
function fixture(t, extraEnv = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'linkd-chart-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const module = path.join(root, 'module');
  const scripts = path.join(module, 'scripts');
  const chart = path.join(module, 'deploy/helm/linkd');
  const bin = path.join(root, 'bin');
  fs.mkdirSync(scripts, { recursive: true });
  fs.mkdirSync(bin);
  fs.cpSync(source, chart, { recursive: true });
  const script = path.join(scripts, 'ci-release.sh');
  fs.copyFileSync(path.join(__dirname, 'ci-release.sh'), script);
  for (const name of ['docker', 'git']) {
    fs.writeFileSync(path.join(bin, name), '#!/bin/sh\necho unexpected-tool >&2\nexit 99\n', { mode: 0o755 });
  }
  fs.writeFileSync(path.join(bin, 'curl'), `#!/usr/bin/env node
const fs = require('node:fs');
fs.writeFileSync(process.env.UPLOAD_CALL, JSON.stringify(process.argv.slice(2)));
process.stdin.resume();
process.stdin.on('end', () => process.exit(process.env.FAIL_UPLOAD ? 22 : 0));
`, { mode: 0o755 });
  const env = { ...process.env };
  for (const key of ['PACKAGE_VERSION', 'IMAGE_REPOSITORY', 'REGISTRY_USER', 'REGISTRY_PASSWORD', 'PEER_REVISION', 'PEER_IMAGE_REPOSITORY', 'GIT_DIR']) delete env[key];
  Object.assign(env, {
    PATH: `${bin}:${process.env.PATH}`, RELEASE_ID: 'chart-test', TARGET_ARCH: 'amd64',
    HELM_UPLOAD_URL: 'https://repo.example/helm/api/project/repository/charts',
    HELM_USER: 'test', HELM_PASSWORD: 'test-chart-token', UPLOAD_CALL: path.join(root, 'upload.json'),
  }, extraEnv);
  const state = path.join(module, `.release/chart-test-${env.TARGET_ARCH}`);
  return { root, chart, state, env, run: () => spawnSync('bash', [script, 'chart'], { env, encoding: 'utf8' }) };
}

function succeeded(result) {
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

test('无镜像构建状态及版本参数时，原样打包仓库 Chart；安装时覆盖两个镜像及架构', t => {
  const f = fixture(t);
  const metadata = YAML.parse(fs.readFileSync(path.join(f.chart, 'Chart.yaml'), 'utf8'));
  const originalValues = fs.readFileSync(path.join(f.chart, 'values.yaml'), 'utf8');
  const result = f.run();
  succeeded(result);
  const archive = path.join(f.state, 'artifacts', `${metadata.name}-${metadata.version}.tgz`);
  const packed = YAML.parse(execFileSync('helm', ['show', 'chart', archive], { encoding: 'utf8' }));
  assert.equal(packed.version, metadata.version);
  assert.equal(packed.appVersion, metadata.appVersion);
  assert.equal(fs.readFileSync(path.join(f.chart, 'values.yaml'), 'utf8'), originalValues);
  const extracted = path.join(f.root, 'extracted');
  fs.mkdirSync(extracted);
  execFileSync('tar', ['-xzf', archive, '-C', extracted]);
  assert.equal(fs.readFileSync(path.join(extracted, 'linkd/values.yaml'), 'utf8'), originalValues);
  for (const arch of ['amd64', 'arm64']) {
    assert.equal(fs.existsSync(path.join(extracted, `linkd/values-${arch}.yaml`)), false);
    const prefix = `project/images-${arch}`;
    const override = path.join(f.root, `${arch}.yaml`);
    fs.writeFileSync(override, YAML.stringify({
      global: { imageRegistry: 'registry.example' }, nodeSelector: { 'kubernetes.io/arch': arch },
      image: { repository: `${prefix}/linkd`, tag: 'manual-server-tag' },
      console: { image: { repository: `${prefix}/linkd-console`, tag: 'manual-console-tag' } },
    }));
    const args = ['external-services', 'clusters', 'console-ingress', 'servicemonitor']
      .flatMap(name => ['-f', path.join(f.chart, 'examples', `${name}.yaml`)]);
    const rendered = execFileSync('helm', ['template', 'linkd', archive, ...args, '-f', override], { encoding: 'utf8' });
    assert.ok(rendered.includes(`registry.example/${prefix}/linkd:manual-server-tag`));
    assert.ok(rendered.includes(`registry.example/${prefix}/linkd-console:manual-console-tag`));
    assert.ok(rendered.includes(`kubernetes.io/arch: ${arch}`));
  }
  const upload = JSON.parse(fs.readFileSync(f.env.UPLOAD_CALL, 'utf8'));
  assert.ok(upload.includes(`chart=@${archive}`));
  assert.equal(upload.at(-1), f.env.HELM_UPLOAD_URL);
  assert.ok(!upload.join(' ').includes(f.env.HELM_PASSWORD));
  assert.ok(!(result.stdout + result.stderr).includes(f.env.HELM_PASSWORD));
});

test('镜像版本、另一架构 SHA 和旧镜像状态不影响 Chart 自身 version/appVersion', t => {
  const f = fixture(t, { PACKAGE_VERSION: 'image-only-tag', IMAGE_REPOSITORY: 'unused', PEER_REVISION: 'different', TARGET_ARCH: 'arm64' });
  const metadataFile = path.join(f.chart, 'Chart.yaml');
  const metadata = YAML.parse(fs.readFileSync(metadataFile, 'utf8'));
  metadata.version = '3.2.1';
  metadata.appVersion = '9.8.7';
  fs.writeFileSync(metadataFile, YAML.stringify(metadata));
  fs.mkdirSync(f.state, { recursive: true });
  fs.writeFileSync(path.join(f.state, 'version'), 'old-image-version');
  fs.writeFileSync(path.join(f.state, 'revision'), 'old-image-commit');
  succeeded(f.run());
  const packed = YAML.parse(execFileSync('helm', ['show', 'chart', path.join(f.state, 'artifacts/linkd-3.2.1.tgz')], { encoding: 'utf8' }));
  assert.equal(packed.version, '3.2.1');
  assert.equal(packed.appVersion, '9.8.7');
  assert.equal(fs.readFileSync(path.join(f.state, 'version'), 'utf8'), 'old-image-version');
});

test('Chart 版本非法时停止打包，不上传', t => {
  const f = fixture(t);
  const file = path.join(f.chart, 'Chart.yaml');
  fs.writeFileSync(file, fs.readFileSync(file, 'utf8').replace(/^version:.+$/m, 'version: invalid'));
  assert.notEqual(f.run().status, 0);
  assert.equal(fs.existsSync(f.env.UPLOAD_CALL), false);
});

test('上传失败返回失败，不报告已上传，也不泄露 token', t => {
  const f = fixture(t, { FAIL_UPLOAD: '1' });
  const result = f.run();
  assert.notEqual(result.status, 0);
  assert.ok(fs.existsSync(f.env.UPLOAD_CALL));
  assert.ok(!result.stdout.includes('Chart uploaded:'));
  assert.ok(!(result.stdout + result.stderr).includes(f.env.HELM_PASSWORD));
});
