# 在 Linkd 模块根目录执行 make console-image，或 docker build -t linkd-console:<tag> console。
ARG NODE_IMAGE=node:24-bookworm-slim
FROM ${NODE_IMAGE} AS dependencies
ARG NPM_REGISTRY=https://registry.npmjs.org
# Corepack 自身获取 pnpm 与 pnpm 获取项目依赖分别配置，避免只替换其中一个入口。
ENV COREPACK_NPM_REGISTRY=${NPM_REGISTRY}
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile --registry="${NPM_REGISTRY}"
# 生产依赖只随依赖清单变化，源码变化不应重复执行裁剪。
FROM dependencies AS production-dependencies
# prune 不接受 --registry；通过 pnpm 的配置参数传入，保持依赖裁剪使用相同的源。
RUN pnpm prune --prod --config.registry="${NPM_REGISTRY}"

FROM dependencies AS builder
COPY index.html tsconfig*.json vite.config.ts ./
COPY src/ ./src/
# 内存参数只影响编译，不参与依赖安装层的缓存。
ARG NODE_BUILD_OPTIONS=--max-old-space-size=3072
RUN NODE_OPTIONS="${NODE_BUILD_OPTIONS}" pnpm build
ARG VERSION=dev
ARG GIT_COMMIT=unknown
RUN VERSION="${VERSION}" GIT_COMMIT="${GIT_COMMIT}" node -e 'require("node:fs").writeFileSync("build-info.json", JSON.stringify({version: process.env.VERSION, git_commit: process.env.GIT_COMMIT}))'

FROM ${NODE_IMAGE}
ENV NODE_ENV=production \
    LINKD_CONFIG=/app/configs/linkd.yaml \
    LINKD_CONSOLE_MODE=server \
    LINKD_CONSOLE_HOST=0.0.0.0 \
    LINKD_CONSOLE_PORT=4399 \
    LINKD_CONSOLE_BASIC_AUTH_ENABLED=true
WORKDIR /app
COPY --from=builder --chown=node:node /app/package.json ./
COPY --from=production-dependencies --chown=node:node /app/node_modules ./node_modules
COPY --from=builder --chown=node:node /app/dist ./dist
COPY --from=builder --chown=node:node /app/dist-server ./dist-server
COPY --from=builder --chown=node:node /app/build-info.json ./build-info.json
ARG VERSION=dev
ARG GIT_COMMIT=unknown
LABEL org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${GIT_COMMIT}"
USER node
EXPOSE 4399
ENTRYPOINT ["node", "dist-server/server/index.js"]
CMD []
