# 在 Linkd 模块根目录执行 make console-image，或 docker build -t linkd-console:<tag> console。
ARG NODE_IMAGE=node:24-bookworm-slim
FROM ${NODE_IMAGE} AS builder
ARG NODE_BUILD_OPTIONS=--max-old-space-size=3072
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN NODE_OPTIONS="${NODE_BUILD_OPTIONS}" pnpm build && pnpm prune --prod

FROM ${NODE_IMAGE}
ENV NODE_ENV=production \
    LINKD_CONFIG=/app/configs/linkd.yaml \
    LINKD_CONSOLE_MODE=server \
    LINKD_CONSOLE_HOST=0.0.0.0 \
    LINKD_CONSOLE_PORT=4399 \
    LINKD_CONSOLE_BASIC_AUTH_ENABLED=true
WORKDIR /app
COPY --from=builder --chown=node:node /app/package.json ./
COPY --from=builder --chown=node:node /app/node_modules ./node_modules
COPY --from=builder --chown=node:node /app/dist ./dist
COPY --from=builder --chown=node:node /app/dist-server ./dist-server
USER node
EXPOSE 4399
CMD ["node", "dist-server/server/index.js"]
