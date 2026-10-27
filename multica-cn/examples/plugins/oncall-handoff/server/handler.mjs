// On-call Handoff's own server. Multica never runs this — the plugin author does.
//
//   node server/handler.mjs
//
// Env: MULTICA_SIGNING_SECRET (the whsec_… shown once when the token was issued)
//      ROTA_TOKEN  (this handler's OWN copy of the rota credential)
//      PORT (default 8789)
//
// Note what is NOT in the request body: `rota_token`. An administrator typed it
// into Settings → Plugins so the host could tell them it is configured, and the
// host encrypted it and never gave it back — not to the surface, not here.
// Multica sends only non-secret config. The handler holds its own copy, which is
// the point: a leaked endpoint leaks no credentials it was not already holding.

import { createServer } from "node:https";
import { readFileSync } from "node:fs";
import { createHmac, timingSafeEqual } from "node:crypto";

function tlsOptions() {
  const cert = process.env.TLS_CERT ?? "dev-cert.pem";
  const key = process.env.TLS_KEY ?? "dev-key.pem";
  try {
    return { cert: readFileSync(cert), key: readFileSync(key) };
  } catch (error) {
    console.error(`Could not read ${cert} / ${key}: ${error.message}`);
    console.error("See examples/plugins/deploy-sentinel/README.md for the openssl one-liner.");
    process.exit(1);
  }
}

const PORT = Number(process.env.PORT ?? 8789);
const SIGNING_SECRET = process.env.MULTICA_SIGNING_SECRET ?? "";
const ROTA_TOKEN = process.env.ROTA_TOKEN ?? "";
const REPLAY_WINDOW_SECONDS = 300;

function verifySignature(rawBody, signature, timestamp) {
  if (!SIGNING_SECRET) return { ok: false, reason: "server has no signing secret configured" };
  if (!signature || !timestamp) return { ok: false, reason: "missing signature headers" };

  // Reject a replay before spending time on the comparison. Multica signs
  // timestamp + body precisely so an old, validly-signed request cannot be
  // resent later.
  const age = Math.abs(Math.floor(Date.now() / 1000) - Number(timestamp));
  if (!Number.isFinite(age) || age > REPLAY_WINDOW_SECONDS) {
    return { ok: false, reason: "timestamp outside the replay window" };
  }

  const expected = createHmac("sha256", SIGNING_SECRET).update(`${timestamp}.${rawBody}`).digest("hex");
  const provided = Buffer.from(signature, "utf8");
  const computed = Buffer.from(expected, "utf8");
  // Length check first: timingSafeEqual throws on a mismatch rather than
  // returning false, and the length is not the secret.
  if (provided.length !== computed.length || !timingSafeEqual(provided, computed)) {
    return { ok: false, reason: "signature mismatch" };
  }
  return { ok: true };
}

/** Stand-in for the rota service this plugin would really call, using its own token. */
async function fileWithRotaService({ rota, to, notes, windowHours }) {
  if (!ROTA_TOKEN) {
    // Said plainly rather than failing obscurely: the fix is on this server, not
    // in Multica, and an author staring at a 500 will look in the wrong place.
    return { ok: false, reason: "ROTA_TOKEN is not set on the handler. Multica does not send it — set it here." };
  }
  // A real handler would fetch rota.example.com with `Authorization: Bearer
  // ${ROTA_TOKEN}` here. The example stops short of the network so it runs with
  // no third-party account.
  return {
    ok: true,
    handoff_id: `ho-${Date.now().toString(36)}`,
    rota,
    to,
    notes,
    window_hours: windowHours,
  };
}

function summarize(filed) {
  return [
    `**On-call handoff** — ${filed.rota} → ${filed.to}`,
    "",
    filed.notes,
    "",
    `Filed as ${filed.handoff_id}, covering the last ${filed.window_hours}h.`,
  ].join("\n");
}

const server = createServer(tlsOptions(), async (req, res) => {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const rawBody = Buffer.concat(chunks).toString("utf8");

  const reply = (status, payload) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(payload));
  };

  const verified = verifySignature(
    rawBody,
    req.headers["x-multica-signature"],
    req.headers["x-multica-timestamp"],
  );
  if (!verified.ok) {
    console.warn(`refused ${req.url}: ${verified.reason}`);
    return reply(401, { error: verified.reason });
  }

  let payload;
  try {
    payload = JSON.parse(rawBody || "{}");
  } catch {
    return reply(400, { error: "body is not valid JSON" });
  }

  const { hook_key: hookKey, trigger, input = {}, config = {} } = payload;
  console.log(`${hookKey} via ${trigger}`);

  // Belt and braces, and worth keeping in an example: if a secret ever appeared
  // in `config`, the host would have broken its own contract and an author
  // should find out from a log line rather than from an incident.
  if ("rota_token" in config) {
    console.error("config carried a secret-typed field; refusing to proceed");
    return reply(500, { error: "unexpected secret in config" });
  }

  if (hookKey !== "file_handoff") {
    return reply(404, { error: `unknown hook ${hookKey}` });
  }

  const to = String(input.to ?? "").trim();
  const notes = String(input.notes ?? "").trim();
  if (to === "" || notes === "") {
    return reply(400, { error: "both `to` and `notes` are required" });
  }

  const filed = await fileWithRotaService({
    rota: String(config.rota_name ?? "unnamed"),
    to,
    notes,
    windowHours: Number(config.handoff_window_hours ?? 12),
  });
  if (!filed.ok) return reply(502, { error: filed.reason });

  // The surface posts the comment on the user's own session, so the handler
  // returns the text rather than writing it back with the callback token. Both
  // routes exist; this one keeps the comment attributed to the person who
  // actually did the handoff.
  return reply(200, { handoff_id: filed.handoff_id, summary: summarize(filed) });
});

server.listen(PORT, () => console.log(`on-call handoff handler on https://127.0.0.1:${PORT}`));
