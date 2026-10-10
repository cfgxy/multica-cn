// RUYI-483 应用层复现探针 v2 — QA 实机轮探针4（QA-2）序列：
// 同一挂载内先发纯文本评论，再上传非图片文件、打字、发送第二条，
// 断言第二条附件绑定 comment_id。临时探针，修复后转正或移除。
// 定位纪律：评论框 PM 用 React fiber 组件链（CommentInput）判定——
// 页面底部还有全站 FloatingChat 的 ChatInput，几何/顺序定位会被劫持。
// 激活纪律：dev 模式 hydration 慢，shell click 可能落在事件系统接上
// 之前被丢弃——点击后必须验证 shell 消失，未消失则重试。
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

test.describe("RUYI-483 orphan-attachment probe v2", () => {
  let api: TestApiClient;
  let issueId: string;
  let issueTitle: string;
  let workspaceSlug: string;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    issueTitle = "RUYI483 Orphan Probe " + Date.now();
    const issue = await api.createIssue(issueTitle);
    issueId = issue.id;
    workspaceSlug = await loginAsDefault(page);
  });

  test.afterEach(async () => {
    if (api) await api.cleanup();
  });

  test("second submission in one mount binds its upload", async ({ page }) => {
    test.setTimeout(120000);
    page.on("request", (r) => {
      const u = r.url();
      if (/\/api\/issues\/.*\/(comments|attachments)/.test(u) && r.method() !== "OPTIONS")
        console.info("[probe] REQ:", r.method(), u.replace(/^https?:\/\/[^/]+/, "").slice(0, 110));
    });
    await page.goto(`/${workspaceSlug}/issues/${issueId}`, { waitUntil: "domcontentloaded" });
    await waitForPageText(page, issueTitle);
    await expect(page.locator("text=Properties")).toBeVisible();

    // 激活评论框（readonly-first shell → 真 ProseMirror），hydration 竞态防御。
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
    if (commentIdx < 0) {
      const pmDump = await page.evaluate(() =>
        Array.from(document.querySelectorAll(".ProseMirror")).map((el, i) => ({
          i,
          ph: el.getAttribute("data-placeholder") ??
            el.querySelector("[data-placeholder]")?.getAttribute("data-placeholder") ?? null,
          y: Math.round(el.getBoundingClientRect().y),
        })));
      console.info("[probe] PMs@no-comment:", JSON.stringify(pmDump));
    }
    expect(commentIdx, "comment ProseMirror should mount after activation").toBeGreaterThanOrEqual(0);
    const editor = page.locator(".ProseMirror").nth(commentIdx);

    // --- 提交 1：纯文本（探针 4 序列第一步）。
    const first = "probe first " + Date.now();
    await editor.click({ force: true });
    await editor.fill(first);
    await expect
      .poll(async () =>
        page.evaluate((idx) => {
          const el = document.querySelectorAll(".ProseMirror")[idx];
          return (el as any).editor?.state?.doc?.textContent ?? "";
        }, commentIdx),
      )
      .toContain("probe first");
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
    console.info("[probe] first comment landed");

    // --- 同一挂载：点通用上传按钮上传非图片文件。
    const fileInput = page.locator('input[type="file"]:not([accept="image/*"])').last();
    await fileInput.setInputFiles({
      name: "notes.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("hello ruyi-483"),
    });
    // 文件卡写入正文（QA 的 DOM 实证步骤）。
    const card = editor.getByText("notes.txt").first();
    await expect(card).toBeVisible({ timeout: 10000 });
    // QA 节奏：真人上传完成后 1-2s 内即打字发送——不等待防抖 emit。
    await page.waitForTimeout(parseInt(process.env.PROBE_SETTLE_MS ?? "0", 10));
    console.info("[probe] doc@after-upload:", JSON.stringify(
      await page.evaluate((idx) => {
        const el = document.querySelectorAll(".ProseMirror")[idx];
        return (el as any).editor?.state?.doc?.textContent?.slice(0, 120);
      }, commentIdx)));

    // --- 打字（真实键盘路径）+ 发送第二条。
    await editor.click({ force: true, position: { x: 8, y: 8 } });
    const second = "probe second " + Date.now();
    await editor.pressSequentially(second, { delay: 15 });
    await page.waitForTimeout(400);
    console.info("[probe] doc@typed:", JSON.stringify(
      await page.evaluate((idx) => {
        const el = document.querySelectorAll(".ProseMirror")[idx];
        return (el as any).editor?.state?.doc?.textContent?.slice(0, 160);
      }, commentIdx)));
    await page.keyboard.press("ControlOrMeta+Enter");
    await page.waitForTimeout(1200);

    // --- DB 断言：两条评论与附件归属。
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const comments = await client.query(
        `SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at ASC`,
        [issueId],
      );
      console.info("[probe] comments:", JSON.stringify(comments.rows.map((r) => r.content.slice(0, 80))));
      const atts = await client.query(
        `SELECT filename, comment_id FROM attachment WHERE issue_id = $1`,
        [issueId],
      );
      console.info("[probe] attachments:", JSON.stringify(atts.rows.map((r) => ({
        filename: r.filename,
        comment_id: r.comment_id,
      }))));
      const mine = atts.rows.find((r) => r.filename === "notes.txt");
      expect(mine, "uploaded attachment should exist").toBeDefined();
      expect(mine!.comment_id, "orphan: second upload not bound to its comment").toBeTruthy();
    } finally {
      await client.end();
    }
  });
});
