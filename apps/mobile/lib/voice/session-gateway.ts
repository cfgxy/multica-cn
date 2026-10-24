/**
 * Gateway-mode wiring for the mobile voice session (RUYI-694 D3) — kept in
 * its own module with zero RN / store imports so the vitest node lane can
 * pin the migration contract (gateway URL shape, first-frame token in the
 * controller options, never the URL) without loading native modules.
 *
 * The transport is a plain RN WebSocket. The gateway owns every gate
 * verdict — rejections arrive as one `{"error","code"}` frame post-upgrade
 * — so a pre-upgrade connect drop is a plain network verdict and degrades
 * generically; no credential semantics to recover client-side.
 */
import { buildVoiceSessionUrl } from "@multica/core/voice";

/** RN WebSocket implementation of the core transport contract. */
export class MobileVoiceTransport {
  private ws: WebSocket | null = null;

  onFrame: ((data: string) => void) | null = null;
  onClose: ((code: number) => void) | null = null;

  connect(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      this.ws = ws;
      let opened = false;
      ws.onopen = () => {
        opened = true;
        resolve();
      };
      ws.onmessage = (event: WebSocketMessageEvent) => {
        this.onFrame?.(typeof event.data === "string" ? event.data : "");
      };
      ws.onclose = (event: WebSocketCloseEvent) => {
        this.ws = null;
        if (opened) {
          this.onClose?.(event.code ?? 1006);
          return;
        }
        reject(new Error("voice gateway connect failed"));
      };
      ws.onerror = () => {
        // onclose always follows; nothing actionable here.
      };
    });
  }

  send(data: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(data);
  }

  close(): void {
    const ws = this.ws;
    this.ws = null;
    if (ws && ws.readyState === WebSocket.OPEN) ws.close(1000);
  }
}

export interface GatewayVoiceSessionConfig {
  url: string;
  mode: "gateway";
  authToken: string;
}

export function buildGatewayVoiceSessionConfig(args: {
  apiUrl: string;
  agentId: string;
  workspaceSlug: string | null | undefined;
  token: string;
  /** RUYI-874 ①: the chat session the voice surface was opened from. */
  chatSessionId?: string | null;
}): GatewayVoiceSessionConfig {
  return {
    url: buildVoiceSessionUrl(args.apiUrl, args.agentId, {
      workspaceSlug: args.workspaceSlug ?? null,
      chatSessionId: args.chatSessionId ?? null,
    }),
    mode: "gateway",
    authToken: args.token,
  };
}
