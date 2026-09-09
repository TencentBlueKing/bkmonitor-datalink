#!/usr/bin/env bash
# 原生 Linux amd64/arm64 构建；由 CI 在 finally 中调用 cleanup。
set -euo pipefail
set +x

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
module=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
: "${RELEASE_ID:?设置流水线唯一 RELEASE_ID}"
[[ $RELEASE_ID =~ ^[a-z0-9][a-z0-9._-]{0,79}$ ]] || fail '非法 RELEASE_ID'
: "${TARGET_ARCH:?设置 amd64 或 arm64}"
case $TARGET_ARCH in amd64|arm64) ;; *) fail 'TARGET_ARCH 必须为 amd64 或 arm64' ;; esac
state="$module/.release/$RELEASE_ID-$TARGET_ARCH"
command=${1:-}

cleanup() {
    [[ -d $state && ! -L $state && ! -L $module/.release ]] || return 0
    touch "$state/images.tsv"
    while IFS=$'\t' read -r ref expected; do
        [[ -n $ref && $expected == sha256:* ]] || continue
        current=$(docker image inspect --format '{{.Id}}' "$ref" 2>/dev/null) || continue
        [[ $current == "$expected" ]] || { printf '保留已被替换的标签：%s\n' "$ref"; continue; }
        # 包括停止的容器，避免删除仍由其他任务引用的镜像。
        containers=$(docker ps -aq --filter "ancestor=$expected") || continue
        [[ -z $containers ]] || { printf '保留容器引用的镜像：%s\n' "$ref"; continue; }
        docker image rm "$ref" || printf 'WARN: 清理失败：%s\n' "$ref" >&2
    done < "$state/images.tsv"
    rm -rf -- "$state"
}
if [[ $command == cleanup ]]; then cleanup; exit 0; fi

: "${PACKAGE_VERSION:?设置 SemVer 版本，例如 0.1.0-ci.123}"
# 同时满足 Helm SemVer 和 Docker tag；不接受 v 前缀及 +build 元数据。
[[ ${#PACKAGE_VERSION} -le 128 && $PACKAGE_VERSION =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || fail '非法 PACKAGE_VERSION'
if [[ $PACKAGE_VERSION == *-* ]]; then
    IFS=. read -r -a prerelease <<< "${PACKAGE_VERSION#*-}"
    for part in "${prerelease[@]}"; do
        [[ ! $part =~ ^0[0-9]+$ ]] || fail 'SemVer 数字预发布标识不能以 0 开头'
    done
fi
: "${IMAGE_REPOSITORY:?设置不含组件名的镜像仓库前缀}"
[[ $IMAGE_REPOSITORY == */* && $IMAGE_REPOSITORY =~ ^[a-z0-9][a-z0-9./:_-]*[a-z0-9]$ ]] || fail '非法 IMAGE_REPOSITORY'
[[ ! -L $module/.release && ! -L $state ]] || fail '构建目录不能是符号链接'
umask 077
mkdir -p "$state"
# 登录配置仅在本次任务目录中存在，不改宿主机已有的 Docker 登录状态。
export DOCKER_CONFIG="$state/docker-config"
mkdir -p "$DOCKER_CONFIG"
revision=$(git -C "$module" rev-parse HEAD)
[[ $revision =~ ^[0-9a-f]{40}$ ]] || fail '无法读取 Git SHA'
if [[ -f $state/revision ]]; then
    [[ $(cat "$state/revision") == "$revision" && $(cat "$state/version") == "$PACKAGE_VERSION" ]] || fail '工作区或版本在构建后发生变化'
else
    printf '%s\n' "$revision" > "$state/revision"
    printf '%s\n' "$PACKAGE_VERSION" > "$state/version"
fi

login() {
    : "${REGISTRY_USER:?设置镜像仓库用户名}"
    : "${REGISTRY_PASSWORD:?通过 CI 凭据注入镜像仓库密码}"
    printf '%s' "$REGISTRY_PASSWORD" | docker login "${IMAGE_REPOSITORY%%/*}" --username "$REGISTRY_USER" --password-stdin
}
record_image() {
    local ref=$1 id
    id=$(docker image inspect --format '{{.Id}}' "$ref")
    printf '%s\t%s\n' "$ref" "$id" >> "$state/images.tsv"
}
local_image() { printf 'linkd-ci-%s-%s-%s:build' "$RELEASE_ID" "$TARGET_ARCH" "$1"; }
release_image() { printf '%s/%s:%s' "$IMAGE_REPOSITORY" "$1" "$PACKAGE_VERSION"; }

case $command in
build)
    rm -f -- "$state/built" "$state/pushed"
    docker buildx version
    daemon=$(docker info --format '{{.OSType}}/{{.Architecture}}')
    case $daemon in linux/x86_64) daemon=linux/amd64 ;; linux/aarch64) daemon=linux/arm64 ;; esac
    [[ $daemon == "linux/$TARGET_ARCH" ]] || fail "需要原生 linux/$TARGET_ARCH 构建机，实际为 $daemon"
    login
    for component in linkd linkd-console; do
        context=$module
        args=(--build-arg "GO_IMAGE=${GO_IMAGE:-golang:1.26.7}" --build-arg "RUNTIME_IMAGE=${RUNTIME_IMAGE:-tencentos/tencentos4-minimal}" --build-arg "GOPROXY=${GOPROXY:-direct}")
        if [[ $component == linkd-console ]]; then
            context=$module/console
            args=(--build-arg "NODE_IMAGE=${NODE_IMAGE:-node:24-bookworm-slim}" --build-arg "NPM_REGISTRY=${NPM_REGISTRY:-https://registry.npmjs.org}")
        fi
        ref=$(local_image "$component")
        docker buildx build --load --platform "linux/$TARGET_ARCH" --progress plain \
            --build-arg "VERSION=$PACKAGE_VERSION" --build-arg "GIT_COMMIT=$revision" \
            "${args[@]}" --tag "$ref" "$context"
        # 只在成功生成镜像后登记；失败重建不能误删已有标签。
        record_image "$ref"
        [[ $(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$ref") == "linux/$TARGET_ARCH" ]] || fail "$component 架构错误"
        actual=$(docker run --rm --network none "$ref" version)
        expected=$(printf 'version: %s\ngit_commit: %s' "$PACKAGE_VERSION" "$revision")
        [[ $actual == "$expected" ]] || fail "$component 版本元数据不匹配"
    done
    touch "$state/built"
    printf '::set-output name=revision::%s\n' "$revision"
    ;;
push)
    rm -f -- "$state/pushed"
    [[ -f $state/built ]] || fail '两个镜像必须先全部构建并验证成功'
    login
    for component in linkd linkd-console; do
        ref=$(release_image "$component")
        docker tag "$(local_image "$component")" "$ref"
        record_image "$ref"
        docker push "$ref"
    done
    touch "$state/pushed"
    printf '::set-output name=revision::%s\n' "$revision"
    ;;
chart)
    [[ -f $state/pushed ]] || fail '镜像尚未全部推送成功'
    # 调用方传入另一架构的输出，阻止同一版本混用两个 Git 提交。
    if [[ -n ${PEER_REVISION:-} || -n ${PEER_IMAGE_REPOSITORY:-} ]]; then
        [[ -n ${PEER_IMAGE_REPOSITORY:-} ]] || fail "缺少另一架构镜像仓库"
        [[ ${PEER_REVISION:-} == "$revision" ]] || fail '两种架构的 Git SHA 不一致，停止 Chart 发布'
    fi
    : "${HELM_UPLOAD_URL:?设置 Chart 上传 API}"
    : "${HELM_USER:?设置 Chart 上传用户名}"
    : "${HELM_PASSWORD:?通过 CI 凭据注入 Chart 上传密码}"
    [[ $HELM_UPLOAD_URL == https://* && $HELM_UPLOAD_URL != *'"'* ]] || fail 'Chart 上传必须使用 HTTPS'
    # curl 配置通过 stdin 传入，避免密码出现在 argv、磁盘文件和日志中。
    [[ $HELM_USER =~ ^[a-zA-Z0-9._-]+$ && $HELM_PASSWORD =~ ^[a-zA-Z0-9._-]+$ ]] || fail '用户名或 token 含不支持的字符'
    chart="$state/chart/linkd"
    mkdir -p "$state/chart" "$state/artifacts"
    rm -rf -- "$chart"
    cp -R "$module/deploy/helm/linkd" "$chart"
    docker run --rm --network none --user "$(id -u):$(id -g)" \
        --mount "type=bind,src=$chart,dst=/chart" \
        -e PACKAGE_VERSION -e IMAGE_REPOSITORY -e TARGET_ARCH -e "PEER_IMAGE_REPOSITORY=${PEER_IMAGE_REPOSITORY:-}" \
        --mount "type=bind,src=$module/scripts/prepare-release-chart.cjs,dst=/prepare.cjs,readonly" \
        -e NODE_PATH=/app/node_modules \
        --entrypoint node "$(local_image linkd-console)" /prepare.cjs
    values=(-f "$chart/examples/external-services.yaml" -f "$chart/examples/clusters.yaml" -f "$chart/examples/console-ingress.yaml" -f "$chart/examples/servicemonitor.yaml")
    helm lint --strict "$chart" "${values[@]}"
    helm template linkd "$chart" "${values[@]}" > "$state/rendered.yaml"
    for arch_values in "$chart"/values-*.yaml; do
        helm template linkd "$chart" "${values[@]}" -f "$arch_values" > /dev/null
    done
    helm package "$chart" --version "$PACKAGE_VERSION" --app-version "$PACKAGE_VERSION" --destination "$state/artifacts"
    archive="$state/artifacts/linkd-$PACKAGE_VERSION.tgz"
    sha256sum "$archive"
    # POST 不自动重试，避免超时后重复上传已成功的版本。
    printf 'user = "%s:%s"\n' "$HELM_USER" "$HELM_PASSWORD" | \
        curl --config - --fail --silent --show-error --connect-timeout 30 --max-time 300 \
        --form "chart=@$archive" "$HELM_UPLOAD_URL"
    printf '\nChart uploaded: linkd-%s.tgz (Git %s)\n' "$PACKAGE_VERSION" "$revision"
    ;;
*) fail '用法：ci-release.sh build|push|chart|cleanup' ;;
esac
