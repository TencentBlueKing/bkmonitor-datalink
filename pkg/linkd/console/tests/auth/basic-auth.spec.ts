import { test, expect } from "@playwright/test";

const prefix = (process.env.LINKD_CONSOLE_AUTH_TEST_BASE_PATH ?? "").replace(
  /\/$/,
  "",
);

test("anonymous requests cannot read static pages or APIs", async ({
  request,
}) => {
  for (const path of ["/", "/config", "/local-api/config"]) {
    const response = await request.get(prefix + path);
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
    const applicationRequests: string[] = [];
    page.on("request", (request) => {
      if (["script", "stylesheet", "fetch"].includes(request.resourceType())) {
        applicationRequests.push(new URL(request.url()).pathname);
      }
    });
    const response = await page.goto(prefix + "/config");
    expect(response?.status()).toBe(200);
    await expect(page.locator("#root")).not.toBeEmpty();
    await expect(
      page.getByText("MYSQL", { exact: false }).first(),
    ).toBeVisible();
    await page.getByRole("link", { name: "处理状态" }).click();
    await expect(page).toHaveURL(new RegExp(prefix + "/overview$"));
    await page
      .getByRole("link", { name: "Configuration", exact: false })
      .click();
    await expect(page).toHaveURL(new RegExp(prefix + "/config$"));
    await page.reload();
    await expect(
      page.getByText("MYSQL", { exact: false }).first(),
    ).toBeVisible();
    const sourceRequest = page.waitForRequest(
      (request) =>
        new URL(request.url()).pathname === prefix + "/local-api/event-sources",
    );
    await page
      .getByRole("link", { name: "Event Sources", exact: false })
      .click();
    await sourceRequest;
    expect(
      applicationRequests.some((path) => path.startsWith(prefix + "/assets/")),
    ).toBe(true);
    expect(
      applicationRequests.every((path) => path.startsWith(prefix + "/")),
    ).toBe(true);
    const api = await context.request.get(prefix + "/local-api/capabilities");
    expect(api.status()).toBe(200);
    expect(await api.text()).not.toContain("test-only-password");
    const missing = await context.request.get(prefix + "/local-api/not-found");
    expect(missing.status()).toBe(404);
  } finally {
    await context.close();
  }
});
