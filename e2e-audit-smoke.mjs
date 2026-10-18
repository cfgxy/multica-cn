import { chromium } from "playwright";

const apiBase = "http://localhost:21801";
const mcpBase = "http://localhost:13001";
const webBase = "http://localhost:13801";
const stamp = Date.now().toString();
const email = `ruyi-784-audit-${stamp}@example.test`;
const slug = `ruyi-784-audit-${stamp}`;

async function request(url, init = {}) {
  const response = await fetch(url, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init.headers ?? {}) },
  });
  const body = await response.text();
  if (!response.ok) throw new Error(`${init.method ?? "GET"} ${url} failed: ${response.status} ${body}`);
  return body ? JSON.parse(body) : undefined;
}

function rpc(id, method, params) {
  return JSON.stringify({ jsonrpc: "2.0", id, method, params });
}

await request(`${apiBase}/auth/send-code`, { method: "POST", body: JSON.stringify({ email }) });
const login = await request(`${apiBase}/auth/verify-code`, { method: "POST", body: JSON.stringify({ email, code: "888888" }) });
const token = login.token;
const auth = { Authorization: `Bearer ${token}` };
const workspace = await request(`${apiBase}/api/workspaces`, {
  method: "POST",
  headers: auth,
  body: JSON.stringify({ name: "RUYI-784 Audit E2E", slug }),
});
await request(`${apiBase}/api/me/onboarding/complete`, {
  method: "POST",
  headers: auth,
  body: JSON.stringify({ completion_path: "skip_existing", workspace_id: workspace.id }),
});
const pat = await request(`${apiBase}/api/tokens`, {
  method: "POST",
  headers: auth,
  body: JSON.stringify({ name: `ruyi-784-audit-${stamp}`, expires_in_days: 1 }),
});
const mcpHeaders = {
  Authorization: `Bearer ${pat.token}`,
  Accept: "application/json, text/event-stream",
};

await request(`${mcpBase}/mcp`, {
  method: "POST",
  headers: mcpHeaders,
  body: rpc(1, "initialize", { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "ruyi-784-e2e", version: "1.0" } }),
});
await request(`${mcpBase}/mcp`, {
  method: "POST",
  headers: mcpHeaders,
  body: rpc(2, "tools/call", { name: "list_issues", arguments: { workspace: workspace.id } }),
});
await request(`${mcpBase}/mcp`, {
  method: "POST",
  headers: mcpHeaders,
  body: rpc(3, "tools/call", { name: "list_files", arguments: { workspace: workspace.id, host: "ruyi-784-unregistered-host", path: "/authorized" } }),
});

const audit = await request(`${apiBase}/api/workspaces/${workspace.id}/audit-events?limit=50`, { headers: auth });
const finishes = audit.events.filter((event) => event.event_type === "mcp.invocation_finished");
const success = finishes.find((event) => event.details.tool_name === "list_issues" && event.details.result_status === "success");
const refusal = finishes.find((event) => event.details.tool_name === "list_files" && event.details.result_status === "refused" && event.details.error_code === "not_found");
if (!success || !refusal) throw new Error(`expected completed success and refusal audit records, got ${JSON.stringify(finishes)}`);

const browser = await chromium.launch({ headless: true });
try {
  const context = await browser.newContext();
  await context.addCookies([{ name: "multica_logged_in", value: "1", url: webBase }]);
  await context.addInitScript((value) => localStorage.setItem("multica_token", value), token);
  const page = await context.newPage();
  await page.goto(`${webBase}/${workspace.slug}/audit`, { waitUntil: "domcontentloaded" });
  try {
    await page.getByRole("heading", { name: "MCP audit" }).waitFor({ timeout: 30000 });
  } catch (error) {
    const text = (await page.locator("body").innerText()).slice(0, 2000);
    throw new Error(`audit page did not render at ${page.url()}: ${text}`, { cause: error });
  }
  const rows = page.locator("tbody");
  await rows.waitFor({ timeout: 30000 });
  const renderedRows = await rows.innerText();
  for (const expected of ["list_issues", "list_files", "success", "refused"]) {
    if (!renderedRows.includes(expected)) throw new Error(`audit table is missing ${expected}: ${renderedRows}`);
  }
  console.log(JSON.stringify({ workspace: workspace.slug, api_audit: "success+refused", web_audit: "success+refused" }));
  await context.close();
} finally {
  await browser.close();
}
