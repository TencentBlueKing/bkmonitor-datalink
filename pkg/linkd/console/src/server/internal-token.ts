import { createHmac } from "node:crypto";

/** 仅供服务端调用；每次请求签发五分钟 JWT，不接受浏览器传入的身份或 Token。 */
export function internalTokenHeaders(
  credentials: { secretKey: string; username: string },
  now: () => number = Date.now,
): Record<string, string> {
  if (!credentials.secretKey.trim() || !credentials.username.trim()) {
    throw new Error("Internal JWT credentials are required");
  }
  const issuedAt = Math.floor(now() / 1000);
  const header = Buffer.from(
    JSON.stringify({ alg: "HS256", typ: "JWT" }),
  ).toString("base64url");
  const payload = Buffer.from(
    JSON.stringify({
      username: credentials.username,
      iat: issuedAt,
      exp: issuedAt + 300,
    }),
  ).toString("base64url");
  const input = `${header}.${payload}`;
  const signature = createHmac("sha256", credentials.secretKey)
    .update(input)
    .digest("base64url");
  const value = `Bearer ${input}.${signature}`;
  if (Buffer.byteLength(value) > 8192)
    throw new Error("Internal JWT header is too large");
  return { "Internal-Token": value };
}
