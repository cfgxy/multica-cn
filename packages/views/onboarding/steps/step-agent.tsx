"use client";

import { useState } from "react";
import { ArrowLeft, Code, Crown } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Card } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import { api } from "@multica/core/api";
import type {
  Agent,
  AgentRuntime,
  CreateAgentRequest,
} from "@multica/core/types";

/**
 * Lightweight agent template. `instructions` seeds the agent's system
 * prompt — a fresh user can ship with these and refine later in settings.
 */
interface AgentTemplate {
  id: string;
  name: string;
  description: string;
  instructions: string;
  icon: typeof Crown;
}

const AGENT_TEMPLATES: AgentTemplate[] = [
  {
    id: "master",
    name: "Master Agent",
    description: "Manages the workspace, assigns tasks, and coordinates work.",
    instructions:
      "You are a Master Agent for this workspace. Your role is to manage and coordinate tasks, triage incoming issues, and ensure work is distributed effectively across the team.",
    icon: Crown,
  },
  {
    id: "coding",
    name: "Coding Agent",
    description: "Checks out code, implements features, and submits PRs.",
    instructions:
      "You are a Coding Agent. Your role is to check out code repositories, implement features and bug fixes based on issue descriptions, write tests, and submit pull requests.",
    icon: Code,
  },
];

/**
 * Onboarding step that creates a user's first agent. Pick a template →
 * tweak the name → Create. The template drives description and
 * instructions; advanced settings live in the post-onboarding agent
 * editor (skills, runtime_config, env). Fresh-user premise: defaults
 * should be good enough to ship immediately.
 *
 * `onSkip` is always rendered here — an agent-less workspace is still a
 * usable workspace (you can browse issues, invite teammates, etc.), and
 * the user can create an agent later from the Agents page.
 */
export function StepAgent({
  runtime,
  onCreated,
  onSkip,
  onBack,
}: {
  runtime: AgentRuntime;
  onCreated: (agent: Agent) => void | Promise<void>;
  onSkip: () => void | Promise<void>;
  onBack?: () => void;
}) {
  const [selectedTemplateId, setSelectedTemplateId] = useState<string | null>(
    null,
  );
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);

  const selectedTemplate =
    AGENT_TEMPLATES.find((t) => t.id === selectedTemplateId) ?? null;

  const handleSelectTemplate = (template: AgentTemplate) => {
    setSelectedTemplateId(template.id);
    // Prefill name from template only if the user hasn't typed their own.
    // Re-picking a template still overwrites — the assumption is they
    // want the new template's name as a fresh starting point.
    setName(template.name);
  };

  const handleCreate = async () => {
    if (!selectedTemplate || !name.trim() || creating) return;
    setCreating(true);
    try {
      const req: CreateAgentRequest = {
        name: name.trim(),
        description: selectedTemplate.description,
        instructions: selectedTemplate.instructions,
        runtime_id: runtime.id,
        visibility: "workspace",
      };
      const agent = await api.createAgent(req);
      onCreated(agent);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to create agent",
      );
      setCreating(false);
    }
  };

  return (
    <div className="flex w-full max-w-md flex-col items-center gap-6">
      {onBack && (
        <button
          type="button"
          onClick={onBack}
          className="flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back
        </button>
      )}
      <div className="flex flex-col gap-3 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">
          Create your first agent
        </h1>
        <p className="text-base text-muted-foreground">
          Pick a template to get started. You can fully customize it later.
        </p>
      </div>

      <div className="grid w-full grid-cols-2 gap-3">
        {AGENT_TEMPLATES.map((template) => {
          const Icon = template.icon;
          const selected = selectedTemplateId === template.id;
          return (
            <Card
              key={template.id}
              role="button"
              tabIndex={0}
              onClick={() => handleSelectTemplate(template)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  handleSelectTemplate(template);
                }
              }}
              className={cn(
                "flex cursor-pointer flex-col gap-2 p-4 transition-colors",
                selected
                  ? "border-primary ring-1 ring-primary"
                  : "hover:border-foreground/20",
              )}
            >
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                <Icon className="h-4 w-4" />
              </div>
              <div className="text-sm font-medium">{template.name}</div>
              <p className="text-xs text-muted-foreground">
                {template.description}
              </p>
            </Card>
          );
        })}
      </div>

      {selectedTemplate && (
        <div className="flex w-full flex-col gap-1.5">
          <Label className="text-xs text-muted-foreground" htmlFor="agent-name">
            Agent name
          </Label>
          <Input
            id="agent-name"
            autoFocus
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Coding Agent"
            onKeyDown={(e) => e.key === "Enter" && handleCreate()}
          />
        </div>
      )}

      <div className="flex flex-col items-center gap-3">
        <Button
          size="lg"
          disabled={!selectedTemplate || !name.trim() || creating}
          onClick={handleCreate}
        >
          {creating ? "Creating..." : "Create agent"}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground"
          onClick={onSkip}
        >
          Skip for now
        </Button>
      </div>
    </div>
  );
}
