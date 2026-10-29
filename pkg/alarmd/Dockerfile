# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2017-2025 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# Build from pkg/alarmd:
#   docker buildx build --build-context alarmd-cli=../alarmd-cli \
#     --build-arg VERSION=<version> --build-arg GIT_COMMIT=<sha> -t alarmd:<tag> .
# VERSION must be the SemVer the image is released as: the alarmd-cli release
# embedded in the binary is built from the alarmd-cli context with the same
# version, and the pack step refuses anything else. The CLI lives outside this
# build context, so it comes in as the named context alarmd-cli.
# The binary lives under /data/bkmonitor, the same layout the in-house release
# pipeline produces, so one chart serves either image. The runtime stage only
# copies files: no shell or useradd is needed there, so RUNTIME_IMAGE can be any
# base, including a distroless one.

ARG NODE_TOOLS_IMAGE=busybox:1.37.0-musl
ARG GO_IMAGE=golang:1.26.7
ARG RUNTIME_IMAGE=tencentos/tencentos4-minimal
FROM ${NODE_TOOLS_IMAGE} AS node-tools
RUN mkdir -p /data/bkmonitor/node-tools && cp /bin/busybox /data/bkmonitor/node-tools/busybox && /data/bkmonitor/node-tools/busybox --install -s /data/bkmonitor/node-tools
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder

ARG GOPROXY=direct

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY --from=alarmd-cli go.mod go.sum /alarmd-cli/
RUN cd /alarmd-cli && go mod download
COPY --from=alarmd-cli . /alarmd-cli/

COPY . .

# RUN uses the ARGs declared before it; version and target are declared after
# the module download so a version bump does not invalidate that layer.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG TARGETOS
ARG TARGETARCH
# The ldflags mirror pkg/alarmd/Makefile: main.version, main.commit and the
# schema version the binary speaks. The alarmd-cli release is packed first, as
# the Makefile does, so the binary embeds it. The directories are created here
# so the runtime stage does not need a shell.
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out/data/bkmonitor /out/etc/alarmd \
 && go run ./clirelease/pack -cli /alarmd-cli -version "${VERSION}" -out clirelease/assets \
 && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT} -X main.schemaVersion=$(cat SCHEMA_VERSION)" \
      -o /out/data/bkmonitor/alarmd ./cmd/alarmd \
 && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" \
      -o /out/data/bkmonitor/alarmd-node-runner ./cmd/alarmd-node-runner

FROM ${RUNTIME_IMAGE}

# /etc/alarmd is where the chart mounts alarmd.yaml; /data/bkmonitor holds the
# binary. Both are owned by the unprivileged uid the process runs as.
COPY --from=builder --chown=65532:65532 /out/data/bkmonitor /data/bkmonitor
COPY --from=node-tools /data/bkmonitor/node-tools /data/bkmonitor/node-tools
COPY --from=builder --chown=65532:65532 /out/etc/alarmd /etc/alarmd

WORKDIR /data/bkmonitor

ARG VERSION=dev
ARG GIT_COMMIT=unknown
LABEL maintainer="Tencent BlueKing" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${GIT_COMMIT}"

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/data/bkmonitor/alarmd"]
CMD ["--config", "/etc/alarmd/alarmd.yaml"]
