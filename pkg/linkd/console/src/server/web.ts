import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

import fastifyStatic from "@fastify/static";
import type { FastifyInstance } from "fastify";

/** registerWeb 在已带路径前缀的作用域注册静态资源和 SPA 页面，不把未知 API 当成页面。 */
export async function registerWeb(
  app: FastifyInstance,
  basePath: string,
  directory = resolve(import.meta.dirname, "../../dist"),
): Promise<void> {
  const template = await readFile(resolve(directory, "index.html"), "utf8");
  if (!template.includes('<base href="/"')) {
    throw new Error("Console index.html is missing its base path placeholder");
  }
  // basePath 在配置入口严格校验；同一镜像在运行时适配不同挂载路径。
  const html = template.replace('<base href="/"', `<base href="${basePath}/"`);
  const sendIndex = (
    _request: unknown,
    reply: import("fastify").FastifyReply,
  ) =>
    reply
      .type("text/html; charset=utf-8")
      .header("Cache-Control", "no-store")
      .send(html);
  await app.register(fastifyStatic, {
    root: resolve(directory, "assets"),
    prefix: "/assets/",
    index: false,
  });
  app.get("/", sendIndex);
  app.get("/index.html", sendIndex);
  app.setNotFoundHandler((request, reply) => {
    const pathname = (request.raw.url ?? "/").split("?")[0];
    const localPath = pathname.slice(basePath.length);
    if (
      (basePath && !pathname.startsWith(`${basePath}/`)) ||
      !["GET", "HEAD"].includes(request.method) ||
      localPath === "/local-api" ||
      localPath.startsWith("/local-api/") ||
      localPath === "/assets" ||
      localPath.startsWith("/assets/")
    ) {
      return reply
        .status(404)
        .send({ error: { code: "not_found", message: "路径不存在" } });
    }
    return sendIndex(request, reply);
  });
}
