# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# 在 pkg/linkd 目录构建：make image
# 或 docker build --build-arg VERSION=<version> -t linkd:<tag> .

ARG GO_IMAGE=golang:1.26.7
ARG RUNTIME_IMAGE=tencentos/tencentos4-minimal
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder

ARG GOPROXY=direct
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" -o /out/linkd ./cmd/linkd

FROM ${RUNTIME_IMAGE}

ARG VERSION=dev
ARG GIT_COMMIT=unknown
LABEL maintainer="Tencent BlueKing" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${GIT_COMMIT}"

RUN mkdir -p /data/linkd/configs \
    && groupadd -r -g 65532 linkd \
    && useradd -r -u 65532 -g linkd -s /bin/false -d /data/linkd linkd \
    && chown -R linkd:linkd /data/linkd

WORKDIR /data/linkd

COPY --from=builder /out/linkd /data/linkd/linkd
RUN chmod 0755 /data/linkd/linkd \
    && chown linkd:linkd /data/linkd/linkd

USER 65532:65532

ENTRYPOINT ["/data/linkd/linkd"]
CMD ["run", "all-in-one", "--config", "/data/linkd/configs/linkd.yaml"]
