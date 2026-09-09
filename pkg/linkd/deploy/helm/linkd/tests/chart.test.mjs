import assert from "node:assert/strict";
import { test, before, after } from "node:test";
import { execFileSync, spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { dirname, resolve, join } from "node:path";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";

const chart = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const root = resolve(chart, "../../..");
const require = createRequire(join(root, "console/package.json"));
const { parse, parseAllDocuments, stringify } = require("yaml");
const base = parse(readFileSync(join(chart, "examples/external-services.yaml"), "utf8"));
let scratch, binary;
before(() => {
  scratch = mkdtempSync(join(tmpdir(), "linkd-helm-"));
  binary = join(scratch, "linkd");
  execFileSync("go", ["build", "-o", binary, "./cmd/linkd"], {cwd: root, stdio: "pipe"});
});
after(() => rmSync(scratch, {recursive: true, force: true}));

function render(values = base, release = "test", namespace = "test") {
  const output = execFileSync("helm", ["template", release, chart, "--namespace", namespace, "-f", "-"], {input: stringify(values), encoding: "utf8"});
  return parseAllDocuments(output).map(doc => doc.toJSON()).filter(Boolean);
}
const deployments = docs => docs.filter(doc => doc.kind === "Deployment");
const byComponent = (docs, role, group) => deployments(docs).find(d =>
  d.metadata.labels["app.kubernetes.io/component"] === role &&
  (!group || d.metadata.labels["linkd/worker-group"] === group));
function configFor(docs, deploy) {
  const volume = deploy.spec.template.spec.volumes.find(v => v.name === "configuration");
  const secret = docs.find(d => d.kind === "Secret" && d.metadata.name === volume.secret.secretName);
  return Buffer.from(secret.data[volume.secret.items[0].key], "base64").toString();
}
function validateConfigs(docs) {
  for (const deploy of deployments(docs)) {
    const yaml = configFor(docs, deploy);
    const path = join(scratch, deploy.metadata.name + ".yaml");
    writeFileSync(path, yaml);
    execFileSync(binary, ["config", "validate", "--config", path], {stdio: "pipe"});
  }
}
test("default three roles, stable selectors and valid runtime configs", () => {
  const docs = render();
  assert.equal(deployments(docs).length, 3);
  assert.equal(docs.filter(d => d.kind === "Secret").length, 3);
  assert.equal(byComponent(docs, "control-plane").spec.strategy.type, "Recreate");
  assert.equal(byComponent(docs, "cleaner").spec.replicas, 2);
  for (const service of docs.filter(d => d.kind === "Service")) {
    const matches = deployments(docs).filter(d => Object.entries(service.spec.selector).every(([k,v]) => d.spec.template.metadata.labels[k] === v));
    assert.equal(matches.length, 1);
  }
  validateConfigs(docs);
});
test("default GHCR release images reach all roles, Console and migration", () => {
  const docs = render({...base, console: {enabled: true, basicAuth: {existingSecret: "linkd-console-auth"}}});
  const prefix = "ghcr.io/tencentblueking/bkmonitor-datalink/";
  for (const role of ["control-plane", "cleaner", "lifecycle", "console"]) {
    const name = role === "console" ? "linkd-console" : "linkd";
    assert.equal(byComponent(docs, role).spec.template.spec.containers[0].image, prefix + name + ":0.1.0");
  }
  assert.equal(docs.find(d => d.kind === "Job").spec.template.spec.containers[0].image, prefix + "linkd:0.1.0");
});
test("explicit image registry, tag and digest override release defaults", () => {
  const digest = "sha256:" + "b".repeat(64);
  const docs = render({...base, global: {imageRegistry: "mirror.example.com"},
    image: {registry: "ignored.example.com", repository: "custom/linkd", tag: "custom", digest},
    console: {enabled: true, image: {repository: "custom/console", tag: "console-test"}, basicAuth: {existingSecret: "linkd-console-auth"}}});
  assert.equal(byComponent(docs, "cleaner").spec.template.spec.containers[0].image, "mirror.example.com/custom/linkd@" + digest);
  assert.equal(byComponent(docs, "console").spec.template.spec.containers[0].image, "mirror.example.com/custom/console:console-test");
  const result = spawnSync("helm", ["template", "test", chart, "-f", "-"], {
    input: stringify({...base, image: {tag: "", digest: ""}}), encoding: "utf8"});
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /image.tag 或 image.digest 必须配置/);
});
test("worker groups isolate labels, replicas, resources and config Secrets", () => {
  const groups = parse(readFileSync(join(chart, "examples/clusters.yaml"), "utf8"));
  // Worker labels accept domain keys that are not legal Kubernetes label names.
  groups.clusters.alarmd.labels["custom worker label"] = "dispatch-only";
  const docs = render({...base, ...groups});
  assert.equal(deployments(docs).length, 5);
  const cleaner = byComponent(docs, "cleaner", "alarmd");
  assert.equal(cleaner.spec.replicas, 1);
  assert.equal(cleaner.spec.template.spec.containers[0].resources.limits.cpu, "2");
  const labelsEnv = cleaner.spec.template.spec.containers[0].env.find(e => e.name === "LINKD_WORKER_LABELS");
  assert.deepEqual(JSON.parse(labelsEnv.value), groups.clusters.alarmd.labels);
  assert.equal(cleaner.spec.template.metadata.labels["custom worker label"], undefined);
  assert.deepEqual(parse(configFor(docs, cleaner)).worker.labels, groups.clusters.alarmd.labels);
  assert.equal(parse(configFor(docs, cleaner)).worker.require_explicit_selector, true);
  const lifecycle = byComponent(docs, "lifecycle", "alarmd");
  assert.equal(cleaner.spec.template.spec.volumes[0].secret.secretName, lifecycle.spec.template.spec.volumes[0].secret.secretName);
  assert.notEqual(cleaner.spec.template.spec.volumes[0].secret.items[0].key, lifecycle.spec.template.spec.volumes[0].secret.items[0].key);
  assert.notEqual(cleaner.spec.template.spec.volumes[0].secret.secretName, byComponent(docs, "cleaner", "default").spec.template.spec.volumes[0].secret.secretName);
  assert.equal(parse(configFor(docs, cleaner)).dispatch.deployment, "test-test");
  validateConfigs(docs);
});
test("existing Secret can share different keys across groups, labels reach actual config loader", () => {
  const values = parse(readFileSync(join(chart, "examples/existing-secrets.yaml"), "utf8"));
  const docs = render(values);
  assert.equal(docs.filter(d => d.kind === "Secret").length, 0);
  const cleaner = byComponent(docs, "cleaner", "alarmd");
  assert.equal(cleaner.spec.template.spec.volumes[0].secret.secretName, "linkd-config");
  assert.equal(cleaner.spec.template.spec.volumes[0].secret.items[0].key, "alarmd-cleaner.yaml");
  const path = join(scratch, "external.yaml");
  writeFileSync(path, stringify({...base.configuration, worker: {labels: {old: "old"}}}));
  const env = cleaner.spec.template.spec.containers[0].env.find(e => e.name === "LINKD_WORKER_LABELS");
  const effective = execFileSync(binary, ["config", "print", "--config", path], {env: {...process.env, LINKD_WORKER_LABELS: env.value}, encoding: "utf8"});
  assert.deepEqual(parse(effective).worker.labels, {pool: "alarmd"});
});
test("Console Ingress uses authenticated Service and server-only secrets", () => {
  const values = parse(readFileSync(join(chart, "examples/console-ingress.yaml"), "utf8"));
  const docs = render({...base, ...values});
  const dev = byComponent(docs, "console");
  const env = Object.fromEntries(dev.spec.template.spec.containers[0].env.map(e => [e.name, e]));
  assert.equal(env.LINKD_CONSOLE_BASIC_AUTH_ENABLED.value, "true");
  assert.equal(env.LINKD_CONSOLE_BASIC_AUTH_PASSWORD.valueFrom.secretKeyRef.name, "linkd-console-auth");
  assert.equal(env.LINKD_WORKER_TOKEN, undefined);
  const ingress = docs.find(d => d.kind === "Ingress");
  assert.equal(ingress.spec.rules[0].http.paths[0].backend.service.name, dev.metadata.name);
  assert.equal(ingress.spec.rules[0].http.paths[0].path, "/");
  assert.equal(ingress.spec.tls[0].secretName, "linkd-console-tls");
  assert.equal(docs.filter(d => d.kind === "Ingress").length, 1);
});
test("zero replicas, metrics disabled, digest, checksum and bounded names", () => {
  const values = {...base, metrics: {enabled: false}, image: {repository: "linkd", digest: "sha256:" + "a".repeat(64)}, clusters: {default: {cleaner: {replicas: 0}}}};
  const docs = render(values, "a".repeat(53));
  assert.equal(byComponent(docs, "cleaner").spec.replicas, 0);
  assert.equal(docs.filter(d => d.kind === "Service").length, 1);
  assert.ok(byComponent(docs, "cleaner").spec.template.spec.containers[0].image.includes("@sha256:"));
  for (const d of docs) assert.ok(d.metadata.name.length <= 63);
  validateConfigs(docs);
  const modified = render({...base, clusters: {default: {labels: {pool: "new"}}}});
  assert.notEqual(byComponent(render(), "cleaner").spec.template.metadata.annotations["checksum/config"], byComponent(modified, "cleaner").spec.template.metadata.annotations["checksum/config"]);
});
test("invalid values fail before installation", () => {
  const cases = [
    {controlPlane: {replicas: 2}},
    {mode: "all-in-one"},
    {clusters: {bad_name: {}}},
    {clusters: {default: {labels: {pool: 12}}}},
    {clusters: {default: {cleaner: {replicas: -1}}}},
    {clusters: {default: {secretKeys: {cleaner: "same.yaml", lifecycle: "same.yaml"}}}},
    {console: {enabled: true, image: {tag: "test"}}},
    {console: {ingress: {enabled: true, hostname: "test.example"}}},
    {metrics: {enabled: false, serviceMonitor: {enabled: true}}},
    {extraEnvVars: [{name: "LINKD_WORKER_LABELS", value: "{}"}]},
    {controlPlane: {existingSecret: "conflict"}},
  ];
  for (const extra of cases) {
    const result = spawnSync("helm", ["template", "test", chart, "-f", "-"], {input: stringify({...base, ...extra}), encoding: "utf8"});
    assert.notEqual(result.status, 0, "unexpected success for " + JSON.stringify(extra));
  }
});

// 模拟升级 revision，不访问 Kubernetes；Helm hook 等待语义由 hook 注解决定。
function migrateResources(values, upgrade = false) {
  const args = ["template", "test", chart, "-f", "-"];
  if (upgrade) args.push("--is-upgrade");
  return parseAllDocuments(execFileSync("helm", args, {input: stringify(values), encoding: "utf8"})).map(d => d.toJSON()).filter(Boolean);
}
test("watch migration has self-contained pre hooks with ordered Secret and ServiceAccount", () => {
  for (const upgrade of [false, true]) {
    const docs = migrateResources(base, upgrade);
    const job = docs.find(d => d.kind === "Job");
    assert.equal(job.metadata.annotations["helm.sh/hook"], "pre-install,pre-upgrade");
    assert.equal(job.spec.template.spec.restartPolicy, "Never");
    assert.equal(job.spec.activeDeadlineSeconds, 300);
    const pod = job.spec.template.spec;
    assert.deepEqual(pod.containers[0].args, ["storage", "migrate", "--config", "/data/linkd/configs/linkd.yaml", "--timeout", "240s"]);
    assert.equal(pod.containers[0].ports, undefined);
    const secret = docs.find(d => d.kind === "Secret" && d.metadata.name === pod.volumes[0].secret.secretName);
    const account = docs.find(d => d.kind === "ServiceAccount" && d.metadata.name === pod.serviceAccountName);
    assert.equal(secret.metadata.annotations["helm.sh/hook-weight"], "-10");
    assert.equal(account.metadata.annotations["helm.sh/hook-weight"], "-20");
    assert.equal(account.automountServiceAccountToken, false);
    assert.equal(secret.metadata.annotations["helm.sh/hook-delete-policy"], "before-hook-creation,hook-succeeded");
    assert.equal(Buffer.from(secret.data["linkd.yaml"], "base64").toString(), configFor(docs, byComponent(docs, "control-plane")));
  }
});
test("async migration uses regular revision Job and config snapshot, disabled emits nothing", () => {
  const docs = render({...base, migrate: {watch: false}});
  const job = docs.find(d => d.kind === "Job");
  assert.match(job.metadata.name, /-migrate-r1$/);
  assert.equal(job.metadata.annotations?.["helm.sh/hook"], undefined);
  const pod = job.spec.template.spec;
  assert.equal(pod.serviceAccountName, byComponent(docs,"control-plane").spec.template.spec.serviceAccountName);
  const secret = docs.find(d => d.kind === "Secret" && d.metadata.name === pod.volumes[0].secret.secretName);
  assert.equal(secret.metadata.annotations?.["helm.sh/hook"], undefined);
  assert.ok(secret.metadata.name.endsWith("-migrate-r1-config"));
  const disabled = render({...base, migrate: {enabled: false}});
  assert.equal(disabled.filter(d => d.kind === "Job").length, 0);
  assert.equal(disabled.filter(d => d.kind === "Secret").length, 2);
});
test("migration mounts pre-existing config and account without creating hook replacements", () => {
  const values = parse(readFileSync(join(chart,"examples/existing-secrets.yaml"),"utf8"));
  values.serviceAccount = {create: false, name: "external-account"};
  const docs = render(values);
  const job = docs.find(d => d.kind === "Job");
  assert.equal(job.spec.template.spec.serviceAccountName,"external-account");
  assert.equal(job.spec.template.spec.volumes[0].secret.secretName,"linkd-config");
  assert.equal(job.spec.template.spec.volumes[0].secret.items[0].key,"control-plane.yaml");
  assert.equal(docs.filter(d => d.kind === "Secret" || d.kind === "ServiceAccount").length,0);
});
test("migration validates bounds and prevents reserved environment overrides", () => {
  for(const migrate of [
    {watch: "false"}, {timeoutSeconds: 0}, {timeoutSeconds: 301}, {backoffLimit: -1},
    {extraEnvVars:[{name:"LINKD_API_TOKEN",value:"bad"}]},
  ]) {
    const result = spawnSync("helm", ["template","test",chart,"-f","-"], {input:stringify({...base,migrate}),encoding:"utf8"});
    assert.notEqual(result.status,0,JSON.stringify(migrate));
  }
});

test("ServiceMonitor discovers every worker group and control-plane metrics endpoint only", () => {
  const clusters = parse(readFileSync(join(chart,"examples/clusters.yaml"),"utf8"));
  const metrics = parse(readFileSync(join(chart,"examples/servicemonitor.yaml"),"utf8"));
  const docs = render({...base,...clusters,...metrics,console:{enabled:true,image:{tag:"test"},basicAuth:{existingSecret:"console-auth"}}});
  const monitor = docs.find(d => d.kind === "ServiceMonitor");
  assert.equal(monitor.metadata.labels.release,"kube-prometheus-stack");
  assert.deepEqual(monitor.spec.namespaceSelector.matchNames,["test"]);
  assert.deepEqual(monitor.spec.targetLabels,["app.kubernetes.io/component","linkd/worker-group"]);
  const selected = docs.filter(d => d.kind === "Service" && Object.entries(monitor.spec.selector.matchLabels).every(([k,v]) => d.metadata.labels[k] === v));
  assert.equal(selected.length,5);
  assert.deepEqual(new Set(selected.map(d => d.metadata.labels["app.kubernetes.io/component"])),new Set(["control-plane","cleaner","lifecycle"]));
  const endpoint = monitor.spec.endpoints[0];
  assert.equal(endpoint.port,"metrics");
  assert.equal(endpoint.path,"/metrics");
  assert.equal(endpoint.scheme,"http");
  assert.equal(endpoint.interval,"30s");
  assert.equal(endpoint.scrapeTimeout,"10s");
  assert.equal(endpoint.honorLabels,false);
  for(const service of selected) {
    const port = service.spec.ports.find(p => p.name === endpoint.port);
    assert.equal(port.port,9464);
    const targets = deployments(docs).filter(d => Object.entries(service.spec.selector).every(([k,v]) => d.spec.template.metadata.labels[k] === v));
    assert.equal(targets.length,1);
    const container = targets[0].spec.template.spec.containers[0];
    assert.equal(container.ports.find(p => p.name === port.targetPort).containerPort,9464);
    const cfg = parse(configFor(docs,targets[0]));
    assert.equal(cfg.telemetry.metrics.exporter,"prometheus");
    assert.equal(cfg.telemetry.metrics.prometheus.listen_address,"0.0.0.0:9464");
  }
});
test("ServiceMonitor supports discovery namespace, annotations, labels and relabeling configuration", () => {
  const sm = {
    enabled:true,namespace:"monitoring",labels:{release:"monitor"},annotations:{owner:"observability"},
    interval:"60s",scrapeTimeout:"20s",honorLabels:true,sampleLimit:12345,targetLimit:100,
    targetLabels:["team"],podTargetLabels:["version"],
    relabelings:[{sourceLabels:["__meta_kubernetes_pod_node_name"],targetLabel:"node"}],
    metricRelabelings:[{sourceLabels:["__name__"],regex:"unused_metric",action:"drop"}],
  };
  const docs = render({...base,commonLabels:{team:"monitoring","linkd/metrics":"true"},commonAnnotations:{managed:"helm"},metrics:{serviceMonitor:sm}});
  const monitor = docs.find(d => d.kind === "ServiceMonitor");
  assert.equal(monitor.metadata.namespace,"monitoring");
  assert.deepEqual(monitor.spec.namespaceSelector.matchNames,["test"]);
  assert.equal(monitor.metadata.labels.team,"monitoring");
  assert.deepEqual(monitor.metadata.annotations,{managed:"helm",owner:"observability"});
  for(const field of ["targetLabels","podTargetLabels","sampleLimit","targetLimit"]) assert.deepEqual(monitor.spec[field],sm[field]);
  for(const field of ["interval","scrapeTimeout","honorLabels","relabelings","metricRelabelings"]) assert.deepEqual(monitor.spec.endpoints[0][field],sm[field]);
  for(const service of docs.filter(d => d.kind === "Service" && d.metadata.name.endsWith("-metrics"))) assert.equal(service.metadata.labels["linkd/metrics"],"true");
  for(const service of docs.filter(d => d.kind === "Service" && !d.metadata.name.endsWith("-metrics"))) assert.equal(service.metadata.labels["linkd/metrics"],undefined);
});
test("ServiceMonitor remains opt-in and rejects invalid configuration", () => {
  assert.equal(render().filter(d => d.kind === "ServiceMonitor").length,0);
  for(const serviceMonitor of [{sampleLimit:-1},{targetLimit:-1},{honorLabels:"true"},{relabelings:"invalid"},{podTargetLabels:[1]}]) {
    const result=spawnSync("helm",["template","test",chart,"-f","-"],{input:stringify({...base,metrics:{serviceMonitor:{enabled:true,...serviceMonitor}}}),encoding:"utf8"});
    assert.notEqual(result.status,0,JSON.stringify(serviceMonitor));
  }
});
