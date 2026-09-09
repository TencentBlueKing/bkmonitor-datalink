import { describe, expect, it } from "vitest";
import { normalizeBasePath } from "./base-path.js";

describe("Console base path", () => {
  it.each([undefined, "", "/"])("normalizes root %s", (value) => {
    expect(normalizeBasePath(value)).toBe("");
  });
  it("preserves a nested deployment path", () => {
    const value = "/kingeye-web-saas--kingeye-web--saas/linkd";
    expect(normalizeBasePath(value)).toBe(value);
  });
  it.each([
    "linkd",
    "//evil.test",
    "/../linkd",
    "/a//b",
    "/a/",
    "/a%2Fb",
    "/a?b",
    '/a"b',
    "/" + "a".repeat(256),
  ])("rejects ambiguous path %s", (value) => {
    expect(() => normalizeBasePath(value)).toThrow("base path must");
  });
});
