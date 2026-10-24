import type { VoiceTransport } from "@multica/core/voice";

/**
 * Browser/Electron WebSocket implementation of the core VoiceTransport
 * contract (RUYI-449). The voice gateway sends text frames only, so binary
 * handling stays out of this class. onClose reports the negotiated close
 * code when the server sent one (voice gateway error frames close with
 * 4400-range codes), which the session controller maps to a degrade reason.
 */
export class BrowserVoiceTransport implements VoiceTransport {
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
      ws.onerror = () => {
        // onclose always follows a failed handshake and reports the code;
        // this only covers the (rare) error-without-close gap. The message
        // stays generic — the browser exposes no richer error surface.
      };
      ws.onmessage = (event) => {
        this.onFrame?.(typeof event.data === "string" ? event.data : "");
      };
      ws.onclose = (event) => {
        this.ws = null;
        if (!opened) {
          reject(new Error(`voice websocket closed (${event.code})`));
        }
        this.onClose?.(event.code);
      };
    });
  }

  send(data: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(data);
    }
  }

  close(): void {
    const ws = this.ws;
    this.ws = null;
    if (!ws) return;
    // A socket that never finished the handshake is aborted, not closed:
    // close() on CONNECTING rejects the connect() promise (browser spec),
    // which is exactly what a late settle of a dismissed session needs.
    // The controller treats that rejection as a no-op when it is already
    // finished, so no degrade or terminal state is emitted (RUYI-866 P1).
    if (ws.readyState === WebSocket.CONNECTING) {
      ws.close();
      return;
    }
    if (ws.readyState === WebSocket.OPEN) {
      ws.close(1000);
    }
  }
}
