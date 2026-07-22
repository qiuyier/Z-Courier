import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: {
    timeout: 10_000,
  },
  fullyParallel: false,
  workers: 1,
  reporter: process.env.CI
    ? [
        ["list"],
        ["html", { open: "never", outputFolder: "playwright-report" }],
      ]
    : "list",
  use: {
    ignoreHTTPSErrors: process.env.ZCOURIER_CONSOLE_IGNORE_HTTPS_ERRORS === "1",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    viewport: { width: 1365, height: 768 },
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        browserName: "chromium",
      },
    },
  ],
});
