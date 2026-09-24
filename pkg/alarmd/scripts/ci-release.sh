#!/usr/bin/env bash
# alarmd 的 CI 发布脚本：原生 Linux amd64/arm64 构建，build → push，finally 里 cleanup。
# 契约与 pkg/linkd/scripts/ci-release.sh 相同，流水线可以照 linkd 的接。
# 版本来源二选一：按 tag 发布时只给 SOURCE_TAG（pkg/alarmd/v<版本>，镜像 tag 就是 <版本>，
# 且 HEAD 必须是该 tag 指向的提交）；不按 tag 发布时给 PACKAGE_VERSION。
# alarmd 没有 chart 子命令：它的 chart 是鲸眼 chart 的一个模块（charts/kingeye/templates/custom/alarmd），
# 不从本仓打包。
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

# SOURCE_TAG 是源码仓的 tag 名（组件构建流水线 ADD_TAG 打的 pkg/alarmd/v<版本>），镜像版本就取 <版本>，
# 不另填：一次发布只有一个版本坐标。
tag_prefix=pkg/alarmd/v
if [[ -n ${SOURCE_TAG:-} ]]; then
    [[ $SOURCE_TAG == "$tag_prefix"?* && $SOURCE_TAG != *[[:space:]]* ]] || fail "SOURCE_TAG 必须是 ${tag_prefix}<版本> 形式：$SOURCE_TAG"
    tag_version=${SOURCE_TAG#"$tag_prefix"}
    : "${PACKAGE_VERSION:=$tag_version}"
    [[ $PACKAGE_VERSION == "$tag_version" ]] || fail "PACKAGE_VERSION $PACKAGE_VERSION 与 SOURCE_TAG $SOURCE_TAG 不一致"
fi
: "${PACKAGE_VERSION:?设置 SemVer 版本（例如 0.2.0-ci.123），或按 tag 发布时设置 SOURCE_TAG}"
# 同时满足 SemVer 和 Docker tag；不接受 v 前缀及 +build 元数据。
[[ ${#PACKAGE_VERSION} -le 128 && $PACKAGE_VERSION =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || fail '非法 PACKAGE_VERSION'
if [[ $PACKAGE_VERSION == *-* ]]; then
    IFS=. read -r -a prerelease <<< "${PACKAGE_VERSION#*-}"
    for part in "${prerelease[@]}"; do
        [[ ! $part =~ ^0[0-9]+$ ]] || fail 'SemVer 数字预发布标识不能以 0 开头'
    done
fi
# 版本号的 major.minor 必须落在仓库 VERSION 声明的那条线上（0.2.x → 0.2.*），
# 不然镜像里 --version 报的版本和代码的版本线对不上。
version_line=$(tr -d '[:space:]' < "$module/VERSION")
[[ $version_line =~ ^([0-9]+)\.([0-9]+)\.x$ ]] || fail "无法解析 VERSION 文件：$version_line"
[[ $PACKAGE_VERSION == "${BASH_REMATCH[1]}.${BASH_REMATCH[2]}."* ]] || fail "PACKAGE_VERSION $PACKAGE_VERSION 不在 VERSION 声明的 $version_line 线上"
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
if [[ -n ${SOURCE_TAG:-} ]]; then
    # 镜像里 --version 报的 commit 必须就是 tag 指向的提交；tag 不在本地仓说明检出没带上它，不猜。
    tag_commit=$(git -C "$module" rev-parse --verify --quiet "refs/tags/$SOURCE_TAG^{commit}") || fail "本地仓没有 tag ${SOURCE_TAG}：检出步骤没有带上它"
    [[ $tag_commit == "$revision" ]] || fail "HEAD $revision 不是 tag $SOURCE_TAG 指向的提交 $tag_commit"
fi
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
local_image() { printf 'alarmd-ci-%s-%s:build' "$RELEASE_ID" "$TARGET_ARCH"; }
release_image() { printf '%s/alarmd:%s' "$IMAGE_REPOSITORY" "$PACKAGE_VERSION"; }

case $command in
build)
    rm -f -- "$state/built" "$state/pushed"
    docker buildx version
    daemon=$(docker info --format '{{.OSType}}/{{.Architecture}}')
    case $daemon in linux/x86_64) daemon=linux/amd64 ;; linux/aarch64) daemon=linux/arm64 ;; esac
    [[ $daemon == "linux/$TARGET_ARCH" ]] || fail "需要原生 linux/$TARGET_ARCH 构建机，实际为 $daemon"
    login
    ref=$(local_image)
    docker buildx build --load --platform "linux/$TARGET_ARCH" --progress plain \
        --build-arg "VERSION=$PACKAGE_VERSION" --build-arg "GIT_COMMIT=$revision" \
        --build-arg "GO_IMAGE=${GO_IMAGE:-golang:1.26.7}" \
        --build-arg "RUNTIME_IMAGE=${RUNTIME_IMAGE:-tencentos/tencentos4-minimal}" \
        --build-arg "GOPROXY=${GOPROXY:-direct}" \
        --tag "$ref" "$module"
    # 只在成功生成镜像后登记；失败重建不能误删已有标签。
    record_image "$ref"
    [[ $(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$ref") == "linux/$TARGET_ARCH" ]] || fail 'alarmd 架构错误'
    # 镜像里的二进制必须报出本次的版本与提交：发布记录靠它回读，chart 的预检也靠它。
    schema=$(tr -d '[:space:]' < "$module/SCHEMA_VERSION")
    actual=$(docker run --rm --network none "$ref" --version)
    expected="alarmd version=$PACKAGE_VERSION commit=$revision schema_version=$schema"
    [[ $actual == "$expected" ]] || fail "alarmd 版本元数据不匹配：$actual"
    touch "$state/built"
    printf '::set-output name=revision::%s\n' "$revision"
    ;;
push)
    rm -f -- "$state/pushed"
    [[ -f $state/built ]] || fail '镜像必须先构建并验证成功'
    login
    ref=$(release_image)
    docker tag "$(local_image)" "$ref"
    record_image "$ref"
    docker push "$ref"
    touch "$state/pushed"
    printf '::set-output name=revision::%s\n' "$revision"
    printf 'Image pushed: %s (Git %s)\n' "$ref" "$revision"
    ;;
chart)
    fail 'alarmd 没有 chart 子命令：它的 chart 是鲸眼 chart 的模块（charts/kingeye/templates/custom/alarmd），随鲸眼 chart 发布'
    ;;
*) fail '用法：ci-release.sh build|push|cleanup' ;;
esac
