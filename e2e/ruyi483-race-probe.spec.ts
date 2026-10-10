// RUYI-483 竞速复现探针 — settle→URL-进-doc 与 submit 的时序观测。
// 机理假设：非图片上传 settle 后，URL 进入 doc（P1 swap 序列化 / P2 append）
// 与 gate 放行（hasUploadingNode=false）不同步；submit 实时读 getMarkdown()，
// 若读取时 doc 序列化尚无 URL，contentReferencesAttachment 过滤丢 id → 孤儿。
// 策略：上传进行中反复提交（gate 拦截无副作用），settle 后第一击以 ±25ms
// 精度落窗；页面内 20ms 采样 fileCard attrs 记录 URL 进 doc 的时点。
// 临时探针，定论后移除。
import { test, expect } from "@playwright/test";
import pg from "pg";
import { createTestApi, loginAsDefault, waitForPageText } from "./helpers";
import type { TestApiClient } from "./fixtures";

const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

// Locate the comment composer's ProseMirror index by its placeholder —
// React component names (the old fiber-walk needle "CommentInput") are
// minified away in production builds, so that probe found nothing on the
// release entry. The placeholder attribute survives minification and is the
// same anchor comments.spec.ts uses.
function findCommentPmIdx(): number {
  return Array.from(document.querySelectorAll(".ProseMirror")).findIndex(
    (el) =>
      (el.getAttribute("data-placeholder") ??
        el.querySelector("[data-placeholder]")?.getAttribute("data-placeholder")) ===
      "Leave a comment...",
  );
}

test.describe("RUYI-483 race probe", () => {
  let api: TestApiClient;
  let issueId: string;
  let issueTitle: string;
  let workspaceSlug: string;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    issueTitle = "RUYI483 Race Probe " + Date.now();
    const issue = await api.createIssue(issueTitle);
    issueId = issue.id;
    workspaceSlug = await loginAsDefault(page);
  });

  test.afterEach(async () => {
    if (api) await api.cleanup();
  });

  test("settle-to-submit race binds or orphans", async ({ page }) => {
    test.setTimeout(180000);
    await page.goto(`/${workspaceSlug}/issues/${issueId}`, { waitUntil: "domcontentloaded" });
    await waitForPageText(page, issueTitle);
    await expect(page.locator("text=Properties")).toBeVisible();

    const shell = page.getByTestId("comment-composer-shell");
    await expect(shell).toBeVisible();
    await page.waitForFunction(() => document.readyState === "complete");
    for (let attempt = 1; attempt <= 5; attempt++) {
      await shell.click();
      await page.waitForTimeout(1500);
      if ((await shell.count()) === 0) break;
    }
    await expect(shell).toHaveCount(0);
    const commentIdx = await page.evaluate(findCommentPmIdx);
    expect(commentIdx, "comment ProseMirror should mount").toBeGreaterThanOrEqual(0);
    const editor = page.locator(".ProseMirror").nth(commentIdx);

    // --- 提交 1：纯文本（同挂载第二提交场景）。
    await editor.click({ force: true });
    await editor.fill("race first " + Date.now());
    await page.keyboard.press("ControlOrMeta+Enter");
    await expect
      .poll(async () => {
        const c = new pg.Client(DATABASE_URL);
        await c.connect();
        try {
          const r = await c.query(`SELECT count(*)::int AS n FROM comment WHERE issue_id = $1`, [issueId]);
          return r.rows[0].n;
        } finally {
          await c.end();
        }
      }, { timeout: 10000 })
      .toBe(1);
    console.info("[race] first comment landed");

    // --- 安装页面内采样器：每 20ms 记录 fileCard 类节点 attrs。
    await page.evaluate((idx) => {
      const el = document.querySelectorAll(".ProseMirror")[idx] as any;
      const ed = el?.editor;
      if (!ed) return;
      const samples: any[] = [];
      const t0 = performance.now();
      const timer = setInterval(() => {
        const doc = ed.state.doc;
        const nodes: any[] = [];
        doc.descendants((n: any) => {
          const nm = String(n.type?.name ?? "");
          if (!["paragraph", "text", "doc", "hardBreak", "mention"].includes(nm)) {
            nodes.push({ nm, ...JSON.parse(JSON.stringify(n.attrs)) });
          }
          return true;
        });
        samples.push({ t: Math.round(performance.now() - t0), nodes, text: doc.textContent.slice(0, 60) });
        if (samples.length >= 250) clearInterval(timer);
      }, 20);
      (window as any).__ruyi483 = {
        samples,
        done: () => { clearInterval(timer); return samples; },
      };
    }, commentIdx);

    // --- 上传非图片文件。
    const fileInput = page.locator('input[type="file"]:not([accept="image/*"])').last();
    await fileInput.setInputFiles({
      name: "race.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("race " + Date.now()),
    });
    const card = editor.getByText("race.txt").first();
    await expect(card).toBeVisible({ timeout: 10000 });
    const uploadT0 = Date.now();

    // --- 竞速：上传中反复提交（gate 拦截无副作用），settle 后第一击落窗。
    let landed = false;
    for (let i = 0; i < 40 && !landed; i++) {
      await page.keyboard.press("ControlOrMeta+Enter");
      await page.waitForTimeout(25);
      const c = new pg.Client(DATABASE_URL);
      await c.connect();
      try {
        const r = await c.query(`SELECT count(*)::int AS n FROM comment WHERE issue_id = $1`, [issueId]);
        landed = r.rows[0].n >= 2;
      } finally {
        await c.end();
      }
    }
    const samples = await page.evaluate(() => (window as any).__ruyi483.done());
    console.info("[race] landed:", landed, "presses:", Math.max(1, Math.ceil((Date.now() - uploadT0) / 25)));

    // --- 采样摘要：fileCard 的 uploading→URL 时序。
    const interesting = samples.filter((s: any) => s.nodes.length > 0 || s.text.length > 0);
    for (const s of interesting.slice(0, 40)) console.info("[race] sample:", JSON.stringify(s));

    // --- DB 断言。
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const comments = await client.query(
        `SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at ASC`, [issueId]);
      console.info("[race] comments:", JSON.stringify(comments.rows.map((r) => r.content.slice(0, 90))));
      const atts = await client.query(
        `SELECT filename, comment_id FROM attachment WHERE issue_id = $1`, [issueId]);
      console.info("[race] attachments:", JSON.stringify(atts.rows.map((r) => ({
        filename: r.filename, comment_id: r.comment_id,
      }))));
      const mine = atts.rows.find((r) => r.filename === "race.txt");
      expect(mine, "uploaded attachment should exist").toBeDefined();
      expect(mine!.comment_id, "orphan reproduced: upload not bound").toBeTruthy();
    } finally {
      await client.end();
    }
  });
});
