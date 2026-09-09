const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { test } = require('node:test');
const YAML = require('yaml');
const source = path.resolve(__dirname, '../deploy/helm/linkd');
for (const arch of ['amd64', 'arm64']) {
  test(`Chart 默认 ${arch}，另一架构覆盖仓库及节点选择`, t => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'linkd-chart-'));
    t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
    const chart = path.join(directory, 'linkd');
    fs.cpSync(source, chart, { recursive: true });
    const original = fs.readFileSync(path.join(source, 'values.yaml'), 'utf8');
    const primary = 'registry.example/project/images-' + arch;
    const peer = arch === 'amd64' ? 'arm64' : 'amd64';
    execFileSync(process.execPath, [path.join(__dirname, 'prepare-release-chart.cjs'), chart], {
      env: { ...process.env, TARGET_ARCH: arch, PACKAGE_VERSION: '1.2.3-ci.4', IMAGE_REPOSITORY: primary,
        PEER_IMAGE_REPOSITORY: 'registry.example/project/images-' + peer },
    });
    const values = YAML.parse(fs.readFileSync(path.join(chart, 'values.yaml'), 'utf8'));
    assert.equal(values.image.registry, 'registry.example');
    assert.equal(values.image.repository, `project/images-${arch}/linkd`);
    assert.equal(values.console.image.repository, `project/images-${arch}/linkd-console`);
    assert.equal(values.image.tag, '1.2.3-ci.4');
    assert.equal(values.nodeSelector['kubernetes.io/arch'], arch);
    assert.deepEqual(values.clusters, YAML.parse(original).clusters);
    assert.equal(fs.readFileSync(path.join(source, 'values.yaml'), 'utf8'), original);
    const override = YAML.parse(fs.readFileSync(path.join(chart, `values-${peer}.yaml`), 'utf8'));
    assert.equal(override.image.repository, `project/images-${peer}/linkd`);
    assert.equal(override.console.image.tag, '1.2.3-ci.4');
    assert.equal(override.nodeSelector['kubernetes.io/arch'], peer);
    const args = ['external-services', 'clusters', 'console-ingress', 'servicemonitor']
      .flatMap(name => ['-f', path.join(chart, 'examples', name + '.yaml')]);
    try { execFileSync('helm', ['lint', '--strict', chart, ...args], { encoding: 'utf8' }); } catch (error) { throw new Error(error.stdout + error.stderr); }
    const rendered = execFileSync('helm', ['template', 'linkd', chart, ...args, '-f', path.join(chart, `values-${peer}.yaml`)], { encoding: 'utf8' });
    assert.ok(rendered.includes(`registry.example/project/images-${peer}/linkd:1.2.3-ci.4`));
    assert.ok(rendered.includes(`registry.example/project/images-${peer}/linkd-console:1.2.3-ci.4`));
    assert.ok(rendered.includes(`kubernetes.io/arch: ${peer}`));
    execFileSync('helm', ['package', chart, '--version', '1.2.3-ci.4', '--app-version', '1.2.3-ci.4', '-d', directory]);
    const metadata = YAML.parse(execFileSync('helm', ['show', 'chart', path.join(directory, 'linkd-1.2.3-ci.4.tgz')], { encoding: 'utf8' }));
    assert.equal(metadata.version, '1.2.3-ci.4');
    assert.equal(metadata.appVersion, '1.2.3-ci.4');
  });
}
