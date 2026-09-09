import { createHash, timingSafeEqual } from "node:crypto";
import { isIP } from "node:net";

import type { FastifyInstance } from "fastify";

/** ServerAccess 明确区分本地回环访问与必须认证的服务部署。 */
export interface ServerAccess {
  mode: "local" | "server";
  basicAuth?: { username: string; password: string };
}

/** loadServerAccess 只接受显式开关，错误中不回显认证输入。 */
export function loadServerAccess(
  env: NodeJS.ProcessEnv = process.env,
): ServerAccess {
  const mode = env.LINKD_CONSOLE_MODE ?? "local";
  if (mode !== "local" && mode !== "server")
    throw new Error("LINKD_CONSOLE_MODE must be local or server");
  const enabled = env.LINKD_CONSOLE_BASIC_AUTH_ENABLED ?? "false";
  if (enabled !== "true" && enabled !== "false")
    throw new Error("LINKD_CONSOLE_BASIC_AUTH_ENABLED must be true or false");
  if (mode === "server" && enabled !== "true")
    throw new Error("server mode requires Basic Auth");
  if (enabled === "false") return { mode };
  const username = env.LINKD_CONSOLE_BASIC_AUTH_USERNAME;
  const password = env.LINKD_CONSOLE_BASIC_AUTH_PASSWORD;
  if (
    !username ||
    !password ||
    username.includes(":") ||
    ["\r", "\n", "\0"].some((char) => (username + password).includes(char)) ||
    Buffer.byteLength(username + ":" + password) > 4096
  )
    throw new Error(
      "Basic Auth requires a valid username and password (at most 4096 bytes)",
    );
  return { mode, basicAuth: { username, password } };
}

/** validateServerAccess 保留本地模式回环约束，禁止绕过服务模式的认证前提。 */
export function validateServerAccess(host: string, access: ServerAccess): void {
  if (access.mode === "server") {
    if (!access.basicAuth?.username || !access.basicAuth.password)
      throw new Error("server mode requires Basic Auth");
    return;
  }
  if (host === "localhost") return;
  const ip = isIP(host);
  if (
    (ip === 4 && host === "127.0.0.1") ||
    (ip === 6 && (host === "::1" || host === "0:0:0:0:0:0:0:1"))
  )
    return;
  throw new Error("server host must be a loopback address in local mode");
}

/** registerBasicAuth 在路由及静态资源之前认证；失败请求不能触发来源管理副作用。 */
export function registerBasicAuth(
  app: FastifyInstance,
  access: ServerAccess,
): void {
  if (!access.basicAuth) return;
  const expected = createHash("sha256")
    .update(`${access.basicAuth.username}:${access.basicAuth.password}`)
    .digest();
  app.addHook("onRequest", async (request, reply) => {
    const header = request.headers.authorization;
    let accepted = false;
    if (header && header.length <= 5500) {
      const match = /^Basic ([A-Za-z0-9+/]+={0,2})$/i.exec(header);
      if (match) {
        const decoded = Buffer.from(match[1], "base64");
        // Buffer 默认宽松解码；再次编码确保拒绝畸形凭据。比较固定长度摘要避免长度泄漏。
        if (decoded.toString("base64") === match[1]) {
          accepted = timingSafeEqual(
            expected,
            createHash("sha256").update(decoded).digest(),
          );
        }
      }
    }
    if (!accepted) {
      return reply
        .code(401)
        .header(
          "WWW-Authenticate",
          'Basic realm="Linkd Console", charset="UTF-8"',
        )
        .header("Cache-Control", "no-store")
        .send({ error: { code: "unauthorized", message: "需要认证" } });
    }
    reply.header("Cache-Control", "no-store");
  });
}
