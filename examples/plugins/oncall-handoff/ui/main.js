// On-call Handoff — a `modal` surface that files a handoff through a hook.
//
// Three things this example exists to demonstrate, none of which the panel
// examples cover:
//
//   1. A `modal` surface. Same sandboxed iframe as a panel, same bridge, same
//      opaque origin — it differs only in where the host mounts it. It opens
//      because a person picked it from the issue menu; a plugin cannot open it
//      on its own initiative.
//   2. A `ui`-triggered hook. The surface never calls rota.example.com itself.
//      It asks the host, and the host makes the call: signed, rate limited,
//      bounded to the granted `net:` domains, and recorded as an invocation the
//      admin can read afterwards. A surface fetching its own backend would have
//      none of that.
//   3. Write-only secrets. `rota_token` is declared in the manifest and set by
//      an admin, and it is NOT in `context.config` below — the host strips
//      secret-typed fields before the frame ever sees them, and strips them
//      from the http hook body too. It is the author's own credential for the
//      author's own service, and server/handler.mjs reads it from its own env.
//      If you find yourself wanting the token in here, the call you are writing
//      belongs in the hook.

const pending = new Map();
const port = globalThis.__multicaPluginBridgePortV2;
let sequence = 0;

if (!(port instanceof MessagePort)) throw new Error("Multica surface bridge is unavailable");
delete globalThis.__multicaPluginBridgePortV2;
port.onmessage = (message) => {
  const payload = message.data;
  if (payload?.kind === "theme") return applyTheme(payload.theme);
  const entry = pending.get(payload?.id);
  if (!entry) return;
  pending.delete(payload.id);
  if (payload.ok) entry.resolve(payload.data);
  else entry.reject(Object.assign(new Error(payload.error), { status: payload.status }));
};
port.start();
boot();

function applyTheme(theme) {
  for (const [name, value] of Object.entries(theme ?? {})) {
    document.documentElement.style.setProperty(name, value);
  }
}

function call(method, path, body) {
  const id = `r${++sequence}`;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    port.postMessage({ id, kind: "action", method, path, body });
  });
}

function resize() {
  port.postMessage({ id: `z${Date.now()}`, kind: "ui.resize", height: document.body.scrollHeight + 8 });
}

// --- business logic -------------------------------------------------------

/** Remembers who this workspace handed off to last, to prefill the field. */
const LAST_HANDOFF_KEY = "handoff:last-recipient";

/**
 * Runs one of THIS plugin's hooks through the host.
 *
 * `trigger: "ui"` is the surface saying so honestly; the host checks it against
 * the manifest and refuses a hook that did not declare it. The credential the
 * far end needs is attached by the host, not by this frame.
 */
function invokeHook(hookKey, issueId, input) {
  return call("POST", `/hooks/${encodeURIComponent(hookKey)}`, {
    trigger: "ui",
    issue_id: issueId,
    input,
  });
}

// --- rendering ------------------------------------------------------------

const STYLES = `
.wrap { padding: 12px 14px; display: grid; gap: 10px; }
.field { display: grid; gap: 4px; }
.muted { color: var(--muted-foreground); font-size: var(--text-caption, 12px); }
label { font-weight: 500; }
input, textarea {
  width: 100%; box-sizing: border-box; font: inherit;
  padding: 6px 8px; border-radius: var(--radius, 6px);
  border: 1px solid var(--border); background: var(--background); color: var(--foreground);
}
textarea { min-height: 96px; resize: vertical; }
.foot { display: flex; align-items: center; gap: 10px; border-top: 1px solid var(--border); padding-top: 10px; }
button {
  padding: 5px 10px; border-radius: var(--radius, 6px);
  border: 1px solid var(--border); background: var(--background); color: var(--foreground);
  font: inherit; cursor: pointer;
}
button:disabled { opacity: .5; cursor: not-allowed; }
`;

function installStyles() {
  const style = document.createElement("style");
  style.textContent = STYLES;
  document.head.appendChild(style);
}

const el = (id) => document.getElementById(id);

function escapeHtml(value) {
  return String(value).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
}

function fail(message) {
  el("root").innerHTML = `<div class="wrap"><div class="muted">${escapeHtml(message)}</div></div>`;
  resize();
}

async function boot() {
  installStyles();
  const root = el("root");
  root.innerHTML = `<div class="wrap"><div class="muted">Loading…</div></div>`;
  resize();

  let context;
  try {
    context = await call("GET", "/context");
  } catch (error) {
    return fail(`Could not read context: ${error.message}`);
  }
  if (!context.issue) return fail("Open this from an issue to hand it off.");

  // Proof rather than prose: the secret fields are declared in the manifest and
  // set by an admin, and they are still absent here. Reading them out is the
  // one thing this example will not do.
  if ("rota_token" in (context.config ?? {})) {
    return fail("Refusing to run: a secret reached the frame. Report this — the host must never send one.");
  }

  let lastRecipient = "";
  try {
    const stored = await call("GET", `/storage/workspace/${encodeURIComponent(LAST_HANDOFF_KEY)}`);
    lastRecipient = String(stored?.value ?? "");
  } catch (error) {
    // 404 is the ordinary first-run case, not a failure worth showing.
    if (error.status !== 404) return fail(`Could not read saved state: ${error.message}`);
  }

  render(context, lastRecipient);
}

function render(context, lastRecipient) {
  const rota = String(context.config.rota_name ?? "");
  const windowHours = Number(context.config.handoff_window_hours ?? 0);

  el("root").innerHTML = `
    <div class="wrap">
      <div class="muted">
        ${escapeHtml(context.issue.identifier)} · rota <strong>${escapeHtml(rota)}</strong>
        · last ${escapeHtml(String(windowHours))}h
      </div>
      <div class="field">
        <label for="to">Handing off to</label>
        <input id="to" value="${escapeHtml(lastRecipient)}" placeholder="name or rota handle">
      </div>
      <div class="field">
        <label for="notes">What the incoming engineer needs to know</label>
        <textarea id="notes" placeholder="Open incidents, anything mid-flight, anything deliberately left alone."></textarea>
      </div>
      <div class="foot">
        <button id="file">File handoff</button>
        <span id="status" class="muted"></span>
      </div>
      <p class="muted">
        The rota token is set in Settings → Plugins and is never sent to this
        form. Multica signs the call to rota.example.com on this plugin's
        behalf; the handler on the far side holds its own copy of the token.
      </p>
    </div>`;

  const status = (text) => { el("status").textContent = text; resize(); };

  el("file").addEventListener("click", async () => {
    const to = el("to").value.trim();
    const notes = el("notes").value.trim();
    if (to === "") return status("Say who is taking over.");
    if (notes === "") return status("An empty handoff is worse than none.");

    el("file").disabled = true;
    status("Filing…");
    try {
      const result = await invokeHook("file_handoff", context.issue.id, { to, notes });
      // The host's verdict, not the endpoint's: a non-ok status means the call
      // did not complete, whatever the far side may have started doing.
      if (result.status !== "ok") {
        el("file").disabled = false;
        return status(result.error ? `Rota service: ${result.error}` : `Handoff failed (${result.status}).`);
      }

      await call("PUT", `/storage/workspace/${encodeURIComponent(LAST_HANDOFF_KEY)}`, { value: to });

      const summary = typeof result.output?.summary === "string" ? result.output.summary : "";
      if (summary !== "") {
        // Lands as the signed-in user, attributed to this plugin.
        await call("POST", `/issues/${encodeURIComponent(context.issue.id)}/comments`, { content: summary });
      }
      status("Handoff filed.");
    } catch (error) {
      el("file").disabled = false;
      // A 403 here means the admin did not grant comments:write — a different
      // fix from a network failure, so it is surfaced verbatim.
      status(error.message);
    }
  });

  resize();
}
