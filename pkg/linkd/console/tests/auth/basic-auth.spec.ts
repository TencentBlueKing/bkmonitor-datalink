import { test, expect } from "@playwright/test";

test("anonymous requests cannot read static pages or APIs", async ({
  request,
}) => {
  for (const path of ["/", "/config", "/local-api/config"]) {
    const response = await request.get(path);
    expect(response.status()).toBe(401);
    expect(response.headers()["www-authenticate"]).toContain("Basic");
  }
});

test("browser authenticates, loads production assets and refreshes SPA route", async ({
  browser,
  baseURL,
}) => {
  const context = await browser.newContext({
    baseURL,
    httpCredentials: {
      username: "browser-test",
      password: "test-only-password",
    },
  });
  try {
    const page = await context.newPage();
    const response = await page.goto("/config");
    expect(response?.status()).toBe(200);
    await expect(page.locator("#root")).not.toBeEmpty();
    await expect(
      page.getByText("MYSQL", { exact: false }).first(),
    ).toBeVisible();
    await page.reload();
    await expect(page.locator("#root")).not.toBeEmpty();
    const api = await context.request.get("/local-api/capabilities");
    expect(api.status()).toBe(200);
    expect(await api.text()).not.toContain("test-only-password");
    const missing = await context.request.get("/local-api/not-found");
    expect(missing.status()).toBe(404);
  } finally {
    await context.close();
  }
});
