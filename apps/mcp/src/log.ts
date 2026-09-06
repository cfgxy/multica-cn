/**
 * Logger for the MCP server.
 *
 * Everything logged goes through token redaction before it reaches the
 * stream — the logger is the single choke point that enforces "no token in
 * logs". Callers must still not pass comment bodies or issue descriptions;
 * the REST client logs method/path/status/duration only.
 */

import { redactUnknown } from "./redact.js";

export interface Logger {
  info(message: string): void;
  error(message: string): void;
}

function write(stream: NodeJS.WritableStream, message: string): void {
  stream.write(`${redactUnknown(message)}\n`);
}

export const stderrLogger: Logger = {
  info(message: string): void {
    write(process.stderr, message);
  },
  error(message: string): void {
    write(process.stderr, message);
  },
};
