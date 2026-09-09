// 只修改发布目录中的 Chart 副本；使用 Console 镜像中已有的 yaml 依赖。
const fs = require("node:fs"), YAML = require("yaml");
const env = process.env;
const directory = process.argv[2] || "/chart";
function images(repo, arch) {
  const slash = repo.indexOf("/");
  const image = name => ({registry: repo.slice(0, slash), repository: repo.slice(slash + 1) + "/" + name, tag: env.PACKAGE_VERSION, digest: ""});
  return {nodeSelector: {"kubernetes.io/arch": arch}, global: {imageRegistry: ""}, image: image("linkd"), console: {image: image("linkd-console")}};
}
const values = YAML.parseDocument(fs.readFileSync(`${directory}/values.yaml`, "utf8"));
const overrides = images(env.IMAGE_REPOSITORY, env.TARGET_ARCH);
values.setIn(["global", "imageRegistry"], "");
for (const [key, value] of Object.entries(overrides.image)) values.setIn(["image", key], value);
values.setIn(["nodeSelector", "kubernetes.io/arch"], env.TARGET_ARCH);
for (const [key, value] of Object.entries(overrides.console.image)) values.setIn(["console", "image", key], value);
fs.writeFileSync(`${directory}/values.yaml`, values.toString());
fs.writeFileSync(`${directory}/values-${env.TARGET_ARCH}.yaml`, YAML.stringify(overrides));
if (env.PEER_IMAGE_REPOSITORY) {
  const peer = env.TARGET_ARCH === "amd64" ? "arm64" : "amd64";
  fs.writeFileSync(`${directory}/values-${peer}.yaml`, YAML.stringify(images(env.PEER_IMAGE_REPOSITORY, peer)));
}
