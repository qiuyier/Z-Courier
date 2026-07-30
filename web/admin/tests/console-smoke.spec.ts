import { expect, test, type Page } from "@playwright/test";

const consoleURL = process.env.ZCOURIER_CONSOLE_BASE_URL ?? "http://127.0.0.1:18084/console/";
const internalToken = process.env.ZCOURIER_CONSOLE_INTERNAL_TOKEN ?? "dev-internal-token";
const expectedRole = process.env.ZCOURIER_CONSOLE_EXPECTED_ROLE ?? "admin";

async function openConsole(page: Page) {
  await page.goto(consoleURL);
  await expect(page.getByText("Z-Courier").first()).toBeVisible();
}

async function login(page: Page) {
  await openConsole(page);
  await page.getByLabel("Internal Token").fill(internalToken);
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("button", { name: "Sign Out" })).toBeVisible();
  await expect(page.getByText(expectedRole).first()).toBeVisible();
}

async function gotoNav(page: Page, name: string, heading: string | RegExp = name) {
  await page.getByRole("button", { name }).click();
  const pageHeading =
    typeof heading === "string"
      ? page.getByRole("heading", { name: heading, exact: true })
      : page.getByRole("heading", { name: heading });
  await expect(pageHeading).toBeVisible();
}

test("shows the login shell before authentication", async ({ page }) => {
  await openConsole(page);

  await expect(page.getByLabel("Internal Token")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign In" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Operations Overview" })).toBeVisible();
});

test("logs in and navigates core console pages", async ({ page }) => {
  await login(page);

  await expect(page.getByRole("heading", { name: "Operations Overview" })).toBeVisible();
  await gotoNav(page, "Routes");
  await gotoNav(page, "Sessions");
  await gotoNav(page, "Messages");
  await expect(page.getByText("Message Cursor")).toBeVisible();
  await expect(page.getByText("page 1")).toBeVisible();
  await expect(page.getByRole("button", { name: "Back" })).toBeDisabled();
  await gotoNav(page, "Audit", "Audit Trail");
  await expect(page.getByText("Audit Cursor")).toBeVisible();
  await expect(page.getByText("page 1")).toBeVisible();
  await expect(page.getByRole("button", { name: "Back" })).toBeDisabled();
  await gotoNav(page, "Checks");
  await gotoNav(page, "Diagnostics");
  const trafficPolicyPanel = page.getByTestId("traffic-policy-diagnostics");
  await expect(trafficPolicyPanel.getByRole("heading", { name: "Traffic Policies" })).toBeVisible();
  await expect(trafficPolicyPanel.getByText("1 configured policy")).toBeVisible();
  await expect(trafficPolicyPanel.getByText("configured", { exact: true }).first()).toBeVisible();
  await expect(trafficPolicyPanel.getByLabel("Traffic policy console-smoke-upstream")).toBeVisible();
  await gotoNav(page, "Overview", "Operations Overview");
});

test("dependency states use consistent icons and issue counts", async ({ page }) => {
  const dependencies = [
    { name: "configured-dependency", status: "configured", reason: "ready" },
    { name: "disabled-dependency", status: "disabled", reason: "intentionally disabled" },
    { name: "missing-dependency", status: "not_configured", reason: "configuration required" },
    { name: "failed-dependency", status: "unavailable", reason: "probe failed" },
  ];

  await page.route("**/internal/admin/overview", async (route) => {
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }
    const overview = await response.json();
    overview.dependencies = dependencies;
    await route.fulfill({ response, json: overview });
  });
  await page.route("**/internal/admin/diagnostics", async (route) => {
    const response = await route.fetch();
    const diagnostics = await response.json();
    diagnostics.dependencies = dependencies;
    await route.fulfill({ response, json: diagnostics });
  });

  await login(page);
  const overviewPanel = page.locator("section").filter({
    has: page.getByRole("heading", { name: "2 attention needed", exact: true }),
  });
  await expect(overviewPanel.getByLabel("configured-dependency: healthy")).toBeVisible();
  await expect(overviewPanel.getByLabel("disabled-dependency: disabled")).toBeVisible();
  await expect(overviewPanel.getByLabel("missing-dependency: needs attention")).toBeVisible();
  await expect(overviewPanel.getByLabel("failed-dependency: unavailable")).toBeVisible();

  await gotoNav(page, "Diagnostics");
  const diagnosticsPanel = page.locator("article").filter({
    has: page.getByRole("heading", { name: "2 issues", exact: true }),
  });
  await expect(diagnosticsPanel.getByLabel("configured-dependency: healthy")).toBeVisible();
  await expect(diagnosticsPanel.getByLabel("disabled-dependency: disabled")).toBeVisible();
  await expect(diagnosticsPanel.getByLabel("missing-dependency: needs attention")).toBeVisible();
  await expect(diagnosticsPanel.getByLabel("failed-dependency: unavailable")).toBeVisible();
});

test("renders sanitized discovery runtime diagnostics", async ({ page }) => {
  await login(page);
  await page.route("**/internal/admin/diagnostics", async (route) => {
    const response = await route.fetch();
    const diagnostics = await response.json();
    diagnostics.upstream.http_route_states = [
      {
        name: "orders-dns",
        target_type: "http",
        status: "degraded",
        consecutive_failures: 1,
        last_reason: "transport",
        updated_at: "2026-07-28T08:30:00Z",
        discovery: {
          type: "dns",
          resolved_endpoints: 3,
          unhealthy_endpoints: 1,
          last_refresh_result: "success",
          last_refresh_duration: "2.1ms",
          last_refresh_at: "2026-07-28T08:29:58Z",
          last_selection_result: "selected",
          last_selection_at: "2026-07-28T08:30:00Z",
          cooldown_skipped_total: 4,
          last_cooldown_skipped_at: "2026-07-28T08:30:00Z",
          last_endpoint_failure_class: "transport",
          last_endpoint_failure_at: "2026-07-28T08:30:00Z",
          last_forward_result: "success",
          last_forward_attempts: 2,
          last_forward_at: "2026-07-28T08:30:00Z",
          last_failover_decision: "succeeded",
          last_failover_at: "2026-07-28T08:30:00Z",
        },
      },
    ];
    await route.fulfill({ response, json: diagnostics });
  });

  await gotoNav(page, "Diagnostics");
  const routeCard = page.locator("article").filter({ has: page.getByRole("heading", { name: "orders-dns" }) });
  await expect(routeCard.getByText("Endpoint Discovery")).toBeVisible();
  await expect(routeCard.getByText("dns runtime")).toBeVisible();
  await expect(routeCard.getByLabel("Resolved endpoints: 3")).toBeVisible();
  await expect(routeCard.getByLabel("Unhealthy endpoints: 1")).toBeVisible();
  await expect(routeCard.locator("dl").getByText("transport", { exact: true })).toBeVisible();
  await expect(routeCard.locator("dl").getByText("succeeded", { exact: true })).toBeVisible();
  await expect(routeCard).not.toContainText("10.0.0.1");
});

test("renders bounded local traffic policy diagnostics", async ({ page }) => {
  await login(page);
  await page.route("**/internal/admin/diagnostics", async (route) => {
    const response = await route.fetch();
    const diagnostics = await response.json();
    diagnostics.traffic_policy = {
      enabled: true,
      mode: "local",
      store_status: "degraded",
      policy_count: 2,
      policy_names: ["standard-upstream", "priority-orders"],
      default_policy: "standard-upstream",
      key_scope: "client_id",
      idle_ttl: "10m0s",
      no_match_total: 14,
      decisions: {
        allowed: 128,
        rate_limited: 9,
        overloaded: 3,
        admission_unavailable: 0,
      },
      last_result: "rate_limited",
      last_state: "depleted",
      last_decision_at: "2026-07-30T09:10:00Z",
      last_success_at: "2026-07-30T09:10:00Z",
      local: {
        live_keys: 8,
        max_keys: 10,
        utilization: 0.8,
      },
      policies: [
        {
          name: "standard-upstream",
          priority: 100,
          key_scope: "client_id",
          capacity: 25,
          refill_tokens: 25,
          refill_interval: "1s",
          selection_total: 103,
          decisions: {
            allowed: 94,
            rate_limited: 9,
            overloaded: 0,
            admission_unavailable: 0,
          },
          last_result: "rate_limited",
          last_state: "depleted",
          last_decision_at: "2026-07-30T09:10:00Z",
        },
        {
          name: "priority-orders",
          priority: 200,
          key_scope: "client_id",
          capacity: 8,
          refill_tokens: 4,
          refill_interval: "500ms",
          selection_total: 37,
          decisions: {
            allowed: 34,
            rate_limited: 0,
            overloaded: 3,
            admission_unavailable: 0,
          },
          last_result: "allowed",
          last_state: "available",
          last_decision_at: "2026-07-30T09:09:58Z",
        },
      ],
    };
    await route.fulfill({ response, json: diagnostics });
  });

  await gotoNav(page, "Diagnostics");
  const panel = page.getByTestId("traffic-policy-diagnostics");
  await expect(panel.getByRole("heading", { name: "Traffic Policies" })).toBeVisible();
  await expect(panel.getByText("2 configured policies")).toBeVisible();
  await expect(panel.getByText("degraded", { exact: true }).first()).toBeVisible();
  await expect(panel.getByText("80.0%")).toBeVisible();
  await expect(panel.getByRole("meter", { name: "Local quota key usage" })).toHaveAttribute("aria-valuenow", "8");

  const decisions = panel.getByLabel("Aggregate traffic policy decisions");
  await expect(decisions.getByText("128", { exact: true })).toBeVisible();
  await expect(decisions.getByText("9", { exact: true })).toBeVisible();
  await expect(decisions.getByText("3", { exact: true })).toBeVisible();

  const standard = panel.getByLabel("Traffic policy standard-upstream");
  await expect(standard.getByText("25 / 1s", { exact: true })).toBeVisible();
  await expect(standard.getByText("rate_limited", { exact: true })).toBeVisible();
  await expect(standard.getByText("Last state", { exact: true })).toBeVisible();
  await expect(standard.getByText("Last decision", { exact: true })).toBeVisible();
  const priority = panel.getByLabel("Traffic policy priority-orders");
  await expect(priority.getByText("4 / 500ms", { exact: true })).toBeVisible();
  await expect(priority.getByText("available", { exact: true })).toBeVisible();
});

test("renders disabled traffic policy diagnostics", async ({ page }) => {
  await login(page);
  await page.route("**/internal/admin/diagnostics", async (route) => {
    const response = await route.fetch();
    const diagnostics = await response.json();
    diagnostics.traffic_policy = {
      enabled: false,
      store_status: "disabled",
      policy_count: 0,
      policy_names: [],
      no_match_total: 0,
      decisions: {
        allowed: 0,
        rate_limited: 0,
        overloaded: 0,
        admission_unavailable: 0,
      },
    };
    await route.fulfill({ response, json: diagnostics });
  });

  await gotoNav(page, "Diagnostics");
  const panel = page.getByTestId("traffic-policy-diagnostics");
  await expect(panel.getByText("Named traffic policies are disabled")).toBeVisible();
  await expect(panel.getByText("disabled", { exact: true })).toHaveCount(2);
  await expect(panel.getByText("Policies", { exact: true })).toBeVisible();
  await expect(panel.getByRole("meter", { name: "Local quota key usage" })).toHaveCount(0);
});

test("renders unavailable Redis traffic policy diagnostics", async ({ page }) => {
  await login(page);
  await page.route("**/internal/admin/diagnostics", async (route) => {
    const response = await route.fetch();
    const diagnostics = await response.json();
    diagnostics.traffic_policy = {
      enabled: true,
      mode: "redis",
      store_status: "unavailable",
      policy_count: 1,
      policy_names: ["shared-upstream"],
      key_scope: "client_id",
      idle_ttl: "10m0s",
      failure_mode: "fail_closed",
      no_match_total: 2,
      decisions: {
        allowed: 47,
        rate_limited: 6,
        overloaded: 0,
        admission_unavailable: 4,
      },
      last_result: "admission_unavailable",
      last_state: "store_unavailable",
      last_decision_at: "2026-07-30T10:15:00Z",
      last_success_at: "2026-07-30T10:14:58Z",
      last_unavailable_at: "2026-07-30T10:15:00Z",
      policies: [
        {
          name: "shared-upstream",
          priority: 100,
          key_scope: "client_id",
          capacity: 100,
          refill_tokens: 100,
          refill_interval: "1s",
          selection_total: 57,
          decisions: {
            allowed: 47,
            rate_limited: 6,
            overloaded: 0,
            admission_unavailable: 4,
          },
          last_result: "admission_unavailable",
          last_state: "store_unavailable",
          last_decision_at: "2026-07-30T10:15:00Z",
        },
      ],
    };
    await route.fulfill({ response, json: diagnostics });
  });

  await gotoNav(page, "Diagnostics");
  const panel = page.getByTestId("traffic-policy-diagnostics");
  await expect(panel.getByText("redis", { exact: true }).first()).toBeVisible();
  await expect(panel.getByText("unavailable", { exact: true }).first()).toBeVisible();
  await expect(panel.getByText("Redis failure mode")).toBeVisible();
  await expect(panel.getByText("fail_closed", { exact: true })).toBeVisible();
  await expect(panel.getByText("admission_unavailable", { exact: true }).first()).toBeVisible();
  await expect(panel.getByRole("meter", { name: "Local quota key usage" })).toHaveCount(0);
});

test("operator mutation actions are guarded by confirmation dialogs", async ({ page }) => {
  test.skip(expectedRole === "readonly", "readonly role has a dedicated disabled-actions smoke test");

  await login(page);
  await gotoNav(page, "Messages");

  await page.getByRole("button", { name: "Run Retry Scan" }).click();
  const retryDialog = page.getByRole("dialog", { name: "Run Retry Scan" });
  await expect(retryDialog).toBeVisible();
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("dialog", { name: "Run Retry Scan" })).toHaveCount(0);
  await page.getByRole("button", { name: "Run Retry Scan" }).click();
  await page.getByRole("dialog", { name: "Run Retry Scan" }).getByRole("button", { name: "Run Retry Scan" }).click();
  await expect(page.getByText("Scan completed")).toBeVisible();

  const pushPanel = page.locator("article").filter({ has: page.getByRole("heading", { name: "Push Playground" }) });
  await pushPanel.getByLabel("ClientID").fill("console-smoke-client");
  await pushPanel.getByLabel("DeviceID").fill("console-smoke-device");
  await pushPanel.getByLabel("MsgID").fill("2001");
  await pushPanel.getByLabel("Body").fill("console smoke body");
  await pushPanel.getByRole("button", { name: "Send Test Push" }).click();

  const testPushDialog = page.getByRole("dialog", { name: /console-smoke-client \/ console-smoke-device/ });
  await expect(testPushDialog).toBeVisible();
  await expect(page.getByText("Body Preview")).toBeVisible();
  await testPushDialog.getByRole("button", { name: "Send Test Push" }).click();
  await expect(page.getByText("Push Result")).toBeVisible();
  await expect(page.getByText(/queued|sent|delivered|accepted/i).first()).toBeVisible();
});

test("bulk requeue confirms selection and reports partial results", async ({ page }) => {
  test.skip(expectedRole === "readonly", "readonly role has a dedicated disabled-actions smoke test");

  await login(page);
  await page.route("**/internal/messages?**", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      json: {
        code: "ok",
        status: "failed",
        limit: 100,
        has_more: false,
        total: 2,
        messages: [
          { code: "ok", message_id: "failed-console-1", client_id: "client-1", device_id: "device-1", status: "failed" },
          { code: "ok", message_id: "failed-console-2", client_id: "client-2", device_id: "device-2", status: "failed" },
        ],
      },
      status: 200,
    });
  });
  await page.route("**/internal/messages/requeue", async (route) => {
    expect(route.request().postDataJSON()).toEqual({ message_ids: ["failed-console-1", "failed-console-2"] });
    await route.fulfill({
      contentType: "application/json",
      json: {
        code: "partial_failure",
        total: 2,
        success: 1,
        failed: 1,
        results: [
          { code: "ok", message_id: "failed-console-1", status: "pending" },
          {
            code: "queue_capacity_exceeded",
            message_id: "failed-console-2",
            status: "failed",
            reason: "downlink queue capacity exceeded",
          },
        ],
      },
      status: 207,
    });
  });

  await gotoNav(page, "Messages");
  await expect(page.getByLabel("Select message failed-console-1")).toBeVisible();
  await page.getByLabel("Select eligible failed messages").check();
  await expect(page.getByText("2 / 100")).toBeVisible();
  await page.getByRole("button", { name: "Requeue selected" }).click();

  const confirmation = page.getByRole("dialog", { name: "2 failed messages" });
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole("button", { name: "Requeue selected" }).click();

  const result = page.getByRole("dialog", { name: "1 of 2 requeued" });
  await expect(result).toBeVisible();
  await expect(result.getByText("queue_capacity_exceeded")).toBeVisible();
  await result.getByRole("button", { name: "Done" }).click();
  await expect(result).toHaveCount(0);
});

test("readonly sessions keep mutation actions disabled", async ({ page }) => {
  test.skip(expectedRole !== "readonly", "operator/admin role has a dedicated mutation-dialog smoke test");

  await login(page);
  await gotoNav(page, "Messages");

  await expect(page.getByText(/read-only for one or more message operations/i)).toBeVisible();
  await expect(page.getByRole("button", { name: "Run Retry Scan" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Send Test Push" })).toBeDisabled();
  await expect(page.getByLabel("Select eligible failed messages")).toBeDisabled();
  await expect(page.getByRole("button", { name: "Requeue selected" })).toBeDisabled();
});
