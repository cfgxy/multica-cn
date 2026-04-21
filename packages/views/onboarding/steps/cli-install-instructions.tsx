"use client";

import { useState } from "react";
import { Check, Copy, Terminal } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";

const INSTALL_CMD =
  "curl -fsSL https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh | bash";

const CLOUD_API_URL = "https://api.multica.ai";

/**
 * Build the right `multica setup` command for the current deployment.
 *
 *  - Cloud (api.multica.ai) or no apiUrl hint → plain `multica setup`
 *    (the CLI hardcodes the cloud endpoints inside setupCloud).
 *  - Any other apiUrl → `multica setup self-host --server-url ... --app-url ...`
 *    so dev (localhost) and on-prem both land on THIS server, not the
 *    public cloud. Dev is just the localhost case of self-host — no
 *    separate branch needed.
 */
function buildSetupCommand(apiUrl?: string, appUrl?: string): string {
  if (!apiUrl || apiUrl === CLOUD_API_URL) return "multica setup";
  const appPart = appUrl ? ` --app-url ${appUrl}` : "";
  return `multica setup self-host --server-url ${apiUrl}${appPart}`;
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      aria-label="Copy"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-success" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

/**
 * CLI install instructions for the runtime step. Web-only by default —
 * desktop has a bundled daemon that auto-starts, so install guidance is
 * noise there. Rendered as an `instructions` slot on `<StepRuntime />`.
 *
 * The `apiUrl` / `appUrl` props point the setup command at the right
 * server. The web shell passes `process.env.NEXT_PUBLIC_API_URL` and
 * `window.location.origin`; a self-host deployment gets a self-host
 * command; dev gets a self-host command pointing at localhost; cloud
 * gets the plain `multica setup`.
 */
export function CliInstallInstructions({
  apiUrl,
  appUrl,
}: {
  apiUrl?: string;
  appUrl?: string;
}) {
  const setupCmd = buildSetupCommand(apiUrl, appUrl);
  const setupSteps = [
    { label: "Install the Multica CLI", cmd: INSTALL_CMD },
    { label: "Set up and start the daemon", cmd: setupCmd },
  ];

  return (
    <Card className="w-full">
      <CardContent className="space-y-3 pt-4">
        {setupSteps.map((step, i) => (
          <div key={i}>
            <p className="mb-1.5 text-xs text-muted-foreground">
              {i + 1}. {step.label}
            </p>
            <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2.5 font-mono text-sm">
              <Terminal className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <code className="min-w-0 flex-1 whitespace-pre-wrap break-all">
                {step.cmd}
              </code>
              <CopyButton text={step.cmd} />
            </div>
          </div>
        ))}
        <p className="pt-1 text-xs text-muted-foreground">
          <code className="rounded bg-background px-1 py-0.5 font-mono">
            multica setup
          </code>{" "}
          handles authentication, configuration, and daemon startup.
        </p>
      </CardContent>
    </Card>
  );
}
