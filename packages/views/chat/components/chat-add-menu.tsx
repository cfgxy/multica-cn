"use client";

import { useRef, type ReactNode } from "react";
import {
  AtSign,
  Camera,
  Check,
  FileUp,
  FolderKanban,
  Image as ImageIcon,
  MessageSquareText,
  MoreHorizontal,
  Sparkles,
  X,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import type { ChatSession, Project, QuickReply } from "@multica/core/types";
import { ProjectIcon } from "../../projects/components/project-icon";
import { useT } from "../../i18n";
import { ChatSessionModelSubmenu } from "./chat-session-model-selector";

interface ChatAddMenuProps {
  /** Called with each selected file — the caller routes it through the
   *  editor's upload extension, same path as paste / drag-drop. */
  onSelectFile?: (file: File) => void;
  onInsertMention?: () => void;
  onInsertSkill?: () => void;
  quickReplies?: QuickReply[];
  onSelectQuickReply?: (content: string) => void;
  projects?: Project[];
  projectId?: string | null;
  onSelectProject?: (projectId: string | null) => void;
  /** Soft warning: the active agent's daemon is too old to receive the
   *  project description. Selection stays enabled; the submenu only appends
   *  an explanatory hint so the user knows before choosing. */
  projectContextUnsupported?: boolean;
  model?: {
    session: ChatSession;
    wsId: string;
    runtimeId: string;
    agentModel: string | null;
    disabled: boolean;
  };
  anchor?: HTMLElement | null;
  disabled?: boolean;
}

/**
 * The "+" affordance at the bottom-left of the chat composer. Replaces the
 * standalone paperclip button: file upload now lives here as a submenu entry,
 * leaving room for future add-actions (agents, skills, tools) under one entry
 * point without crowding the input bar.
 */
export function ChatAddMenu({
  onSelectFile,
  onInsertMention,
  onInsertSkill,
  quickReplies = [],
  onSelectQuickReply,
  projects = [],
  projectId,
  onSelectProject,
  projectContextUnsupported,
  model,
  anchor,
  disabled,
}: ChatAddMenuProps) {
  const { t } = useT("chat");
  const { t: tCommon } = useT("common");
  const inputRef = useRef<HTMLInputElement>(null);
  const imageRef = useRef<HTMLInputElement>(null);
  const cameraRef = useRef<HTMLInputElement>(null);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    if (files.length === 0) return;
    e.target.value = "";
    for (const file of files) onSelectFile?.(file);
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              disabled={disabled}
              aria-label={t(($) => $.input.add_tooltip)}
              title={t(($) => $.input.add_tooltip)}
              className="rounded-full text-muted-foreground"
            >
              <MoreHorizontal />
            </Button>
          }
        />
        <DropdownMenuContent
          align="start"
          side="top"
          sideOffset={8}
          anchor={anchor}
          className="w-[var(--anchor-width)] rounded-lg border border-surface-border bg-surface-raised p-2 ring-1 ring-surface-border origin-bottom data-[side=top]:slide-in-from-bottom-0 data-open:zoom-in-100 motion-reduce:animate-none motion-reduce:transition-none"
        >
          <DropdownMenuGroup className="grid grid-cols-4 gap-1">
            {onSelectFile && (
              <ActionItem
                icon={<Camera className="size-5" />}
                label={tCommon(($) => $.composer.image_source_camera)}
                onClick={() => cameraRef.current?.click()}
              />
            )}
            {onSelectFile && (
              <ActionItem
                icon={<ImageIcon className="size-5" />}
                label={tCommon(($) => $.composer.image_source_library)}
                onClick={() => imageRef.current?.click()}
              />
            )}
            {onSelectFile && (
              <ActionItem
                icon={<FileUp className="size-5" />}
                label={tCommon(($) => $.composer.upload_file)}
                onClick={() => inputRef.current?.click()}
              />
            )}
            {onInsertMention && (
              <ActionItem
                icon={<AtSign className="size-5" />}
                label={tCommon(($) => $.composer.mention_hint)}
                onClick={onInsertMention}
              />
            )}
            {onInsertSkill && (
              <ActionItem
                icon={<Sparkles className="size-5" />}
                label={tCommon(($) => $.composer.skill_hint)}
                onClick={onInsertSkill}
              />
            )}
            {onSelectQuickReply && (
              <DropdownMenuSub>
                <DropdownMenuSubTrigger className="h-16 flex-col justify-center gap-1 rounded-md px-1 py-2 text-caption text-muted-foreground data-popup-open:bg-accent data-popup-open:text-accent-foreground [&>svg:last-child]:hidden">
                  <MessageSquareText className="size-5" />
                  <span className="max-w-full truncate">
                    {tCommon(($) => $.composer.quick_reply_hint)}
                  </span>
                </DropdownMenuSubTrigger>
                <DropdownMenuSubContent className="max-h-72 min-w-52 overflow-y-auto">
                  {quickReplies.length === 0 ? (
                    <div className="px-2 py-1.5 text-caption text-muted-foreground">
                      {t(($) => $.input.no_quick_replies)}
                    </div>
                  ) : (
                    quickReplies.map((reply) => (
                      <DropdownMenuItem
                        key={reply.id}
                        onClick={() => onSelectQuickReply(reply.content)}
                        className="flex-col items-start gap-0.5"
                      >
                        <span className="w-full truncate text-body font-medium">
                          {reply.name}
                        </span>
                        <span className="w-full truncate text-caption text-muted-foreground">
                          {reply.content.split("\n")[0]}
                        </span>
                      </DropdownMenuItem>
                    ))
                  )}
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            )}
            {onSelectProject && (
              <DropdownMenuSub>
                <DropdownMenuSubTrigger className="h-16 flex-col justify-center gap-1 rounded-md px-1 py-2 text-caption text-muted-foreground data-popup-open:bg-accent data-popup-open:text-accent-foreground [&>svg:last-child]:hidden">
                  <FolderKanban className="size-5" />
                  <span className="max-w-full truncate">
                    {t(($) => $.input.project_context)}
                  </span>
                </DropdownMenuSubTrigger>
                <DropdownMenuSubContent className="max-h-72 min-w-52 overflow-y-auto">
                  {projects.map((project) => (
                    <DropdownMenuItem
                      key={project.id}
                      onClick={() => onSelectProject(project.id)}
                    >
                      <ProjectIcon project={project} size="md" />
                      <span className="min-w-0 flex-1 truncate">
                        {project.title}
                      </span>
                      {project.id === projectId && <Check className="ml-auto" />}
                    </DropdownMenuItem>
                  ))}
                  {projects.length === 0 && (
                    <div className="px-2 py-1.5 text-caption text-muted-foreground">
                      {t(($) => $.input.no_projects)}
                    </div>
                  )}
                  {projectId && <DropdownMenuSeparator />}
                  {projectId && (
                    <DropdownMenuItem onClick={() => onSelectProject(null)}>
                      <X />
                      {t(($) => $.input.remove_project_context)}
                    </DropdownMenuItem>
                  )}
                  {projectContextUnsupported && (
                    <>
                      <DropdownMenuSeparator />
                      <div className="max-w-56 px-2 py-1.5 text-caption text-muted-foreground">
                        {t(($) => $.input.project_context_unsupported)}
                      </div>
                    </>
                  )}
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            )}
            {model && <ChatSessionModelSubmenu {...model} />}
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {onSelectFile && (
        <>
          <input
            ref={cameraRef}
            type="file"
            accept="image/*"
            capture="environment"
            className="hidden"
            onChange={handleChange}
          />
          <input
            ref={imageRef}
            type="file"
            accept="image/*"
            multiple
            className="hidden"
            onChange={handleChange}
          />
          <input
            ref={inputRef}
            type="file"
            multiple
            className="hidden"
            onChange={handleChange}
          />
        </>
      )}
    </>
  );
}

function ActionItem({
  icon,
  label,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  onClick: () => void;
}) {
  return (
    <DropdownMenuItem
      onClick={onClick}
      className="h-16 flex-col justify-center gap-1 rounded-md px-1 py-2 text-caption text-muted-foreground focus:bg-accent focus:text-accent-foreground"
    >
      {icon}
      <span className="max-w-full truncate">{label}</span>
    </DropdownMenuItem>
  );
}
