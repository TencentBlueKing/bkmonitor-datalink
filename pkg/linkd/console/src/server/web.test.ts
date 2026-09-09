// @vitest-environment node
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import Fastify from "fastify";
import { expect, it } from "vitest";
import { registerWeb } from "./web.js";
import { registerBasicAuth } from "./auth.js";

it.each(["", "/kingeye-web-saas--kingeye-web--saas/linkd"])(
  "serves authenticated pages and assets under %s",
  async (basePath) => {
    const directory = await mkdtemp(join(tmpdir(), "linkd-web-"));
    const app = Fastify();
    const headers = {
      authorization: `Basic ${Buffer.from("admin:test-password").toString("base64")}`,
    };
    try {
      await mkdir(join(directory, "assets"));
      await writeFile(
        join(directory, "assets/app.js"),
        "export const ready = true;",
      );
      await writeFile(
        join(directory, "index.html"),
        '<html><head><base href="/" /><script src="./assets/app.js"></script></head></html>',
      );
      registerBasicAuth(app, {
        mode: "server",
        basicAuth: { username: "admin", password: "test-password" },
      });
      await app.register(
        async (routes) => registerWeb(routes, basePath, directory),
        { prefix: basePath },
      );
      for (const route of [
        "/",
        "/index.html",
        "/explore/events?limit=10",
        "/event-sources",
      ]) {
        const result = await app.inject({ url: basePath + route, headers });
        expect(result.statusCode).toBe(200);
        expect(result.body).toContain(`<base href="${basePath}/"`);
      }
      const bare = await app.inject({ url: basePath || "/", headers });
      expect(bare.statusCode).toBe(200);
      for (const route of ["/", "/assets/app.js", "/explore/events"]) {
        expect((await app.inject(basePath + route)).statusCode).toBe(401);
      }
      const asset = await app.inject({
        url: basePath + "/assets/app.js",
        headers,
      });
      expect(asset.statusCode).toBe(200);
      expect(asset.body).toContain("export const ready");
      for (const route of [
        "/local-api/missing",
        "/local-api",
        "/assets/missing.js",
      ]) {
        expect(
          (await app.inject({ url: basePath + route, headers })).statusCode,
        ).toBe(404);
      }
      expect(
        (
          await app.inject({
            url: basePath + "/unknown",
            method: "POST",
            headers,
          })
        ).statusCode,
      ).toBe(404);
      if (basePath) {
        for (const url of [
          "/",
          "/assets/app.js",
          basePath + "-other/overview",
        ]) {
          expect((await app.inject({ url, headers })).statusCode).toBe(404);
        }
      }
    } finally {
      await app.close();
      await rm(directory, { recursive: true, force: true });
    }
  },
);
