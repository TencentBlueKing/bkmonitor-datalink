import { afterEach, expect, it, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { BrowserRouter, Link } from "react-router-dom";
import { consoleBasePath, consoleURL } from "./base-path";
import { getConfigSummary } from "./api";

afterEach(() => {
  cleanup();
  document.querySelector("base")?.remove();
  window.history.replaceState({}, "", "/");
  vi.unstubAllGlobals();
});

it("keeps root API URLs unchanged without a base element", () => {
  expect(consoleURL("/local-api/version")).toBe("/local-api/version");
});

it("prefixes API calls and navigation independently of the current deep URL", async () => {
  const prefix = "/kingeye-web-saas--kingeye-web--saas/linkd";
  const base = document.createElement("base");
  base.href = `${prefix}/`;
  document.head.append(base);
  window.history.replaceState({}, "", `${prefix}/explore/events?limit=10`);
  const fetchMock = vi
    .fn()
    .mockResolvedValue({ ok: false, status: 503, json: async () => ({}) });
  vi.stubGlobal("fetch", fetchMock);
  await expect(getConfigSummary()).rejects.toThrow();
  expect(fetchMock.mock.calls[0][0]).toBe(`${prefix}/local-api/config`);
  render(
    <BrowserRouter basename={consoleBasePath()}>
      <Link to="/event-sources">来源</Link>
    </BrowserRouter>,
  );
  expect(screen.getByRole("link", { name: "来源" })).toHaveAttribute(
    "href",
    `${prefix}/event-sources`,
  );
});
