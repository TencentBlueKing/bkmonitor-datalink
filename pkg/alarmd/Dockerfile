# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2017-2025 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# Build from pkg/alarmd:
#   docker buildx build --build-arg VERSION=<version> --build-arg GIT_COMMIT=<sha> -t alarmd:<tag> .
# The binary lives under /data/bkmonitor, the same layout the in-house release
# pipeline produces, so one chart serves either image. The runtime stage only
# copies files: no shell or useradd is needed there, so RUNTIME_IMAGE can be any
# base, including a distroless one.

ARG GO_IMAGE=golang:1.26.7
ARG RUNTIME_IMAGE=tencentos/tencentos4-minimal
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder

ARG GOPROXY=direct

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# RUN uses the ARGs declared before it; version and target are declared after
# the module download so a version bump does not invalidate that layer.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG TARGETOS
ARG TARGETARCH
# The ldflags mirror pkg/alarmd/Makefile: main.version, main.commit and the
# schema version the binary speaks. The directories are created here so the
# runtime stage does not need a shell.
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out/data/bkmonitor /out/etc/alarmd \
 && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT} -X main.schemaVersion=$(cat SCHEMA_VERSION)" \
      -o /out/data/bkmonitor/alarmd ./cmd/alarmd

FROM ${RUNTIME_IMAGE}

# /etc/alarmd is where the chart mounts alarmd.yaml; /data/bkmonitor holds the
# binary. Both are owned by the unprivileged uid the process runs as.
COPY --from=builder --chown=65532:65532 /out/data/bkmonitor /data/bkmonitor
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
