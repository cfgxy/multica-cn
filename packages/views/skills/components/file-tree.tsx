"use client";

import { useMemo, useRef, useState } from "react";
import {
  ChevronRight,
  ChevronDown,
  FileText,
  File,
  Folder,
  FolderOpen,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

// ---------------------------------------------------------------------------
// Tree data structures
// ---------------------------------------------------------------------------

interface FileTreeNode {
  name: string;
  path: string;
  isDirectory: boolean;
  children: FileTreeNode[];
}

function buildTree(filePaths: string[]): FileTreeNode[] {
  const root: FileTreeNode[] = [];

  for (const filePath of filePaths) {
    const parts = filePath.split("/");
    let current = root;

    for (let i = 0; i < parts.length; i++) {
      const name = parts[i]!;
      const isLast = i === parts.length - 1;
      const path = parts.slice(0, i + 1).join("/");

      let existing = current.find((n) => n.name === name);

      if (!existing) {
        existing = {
          name,
          path,
          isDirectory: !isLast,
          children: [],
        };
        current.push(existing);
      }

      if (!isLast) {
        current = existing.children;
      }
    }
  }

  function sortNodes(nodes: FileTreeNode[]): FileTreeNode[] {
    nodes.sort((a, b) => {
      if (a.path === "SKILL.md") return -1;
      if (b.path === "SKILL.md") return 1;
      if (a.isDirectory !== b.isDirectory) return a.isDirectory ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    for (const node of nodes) {
      if (node.isDirectory) sortNodes(node.children);
    }
    return nodes;
  }

  return sortNodes(root);
}

function getFileIcon(name: string) {
  if (name.endsWith(".md") || name.endsWith(".mdx")) return FileText;
  return File;
}

// ---------------------------------------------------------------------------
// Tree node renderer
// ---------------------------------------------------------------------------

interface TreeItemContext {
  selectedPath: string;
  focusPath: string;
  collapsed: ReadonlySet<string>;
  onSelect: (path: string) => void;
  onToggleDir: (path: string) => void;
  onFocusItem: (path: string) => void;
  registerItem: (path: string, el: HTMLButtonElement | null) => void;
}

function TreeNodeItem({
  node,
  ctx,
  depth = 0,
}: {
  node: FileTreeNode;
  ctx: TreeItemContext;
  depth?: number;
}) {
  const isSelected = node.path === ctx.selectedPath;
  // Roving tabindex: exactly one item in the tree is tabbable.
  const tabIndex = node.path === ctx.focusPath ? 0 : -1;

  if (node.isDirectory) {
    const expanded = !ctx.collapsed.has(node.path);
    const FolderIcon = expanded ? FolderOpen : Folder;
    const ChevronIcon = expanded ? ChevronDown : ChevronRight;

    return (
      <div>
        <button
          type="button"
          role="treeitem"
          aria-expanded={expanded}
          aria-selected={false}
          tabIndex={tabIndex}
          ref={(el) => ctx.registerItem(node.path, el)}
          onClick={() => ctx.onToggleDir(node.path)}
          onFocus={() => ctx.onFocusItem(node.path)}
          className="flex w-full items-center gap-1.5 py-1 text-left text-xs hover:bg-accent/50 rounded-sm"
          style={{ paddingLeft: `${depth * 12 + 8}px` }}
        >
          <ChevronIcon className="h-3 w-3 shrink-0 text-muted-foreground" />
          <FolderIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{node.name}</span>
        </button>
        {expanded && (
          <div role="group">
            {node.children.map((child) => (
              <TreeNodeItem
                key={child.path}
                node={child}
                ctx={ctx}
                depth={depth + 1}
              />
            ))}
          </div>
        )}
      </div>
    );
  }

  const Icon = getFileIcon(node.name);

  return (
    <button
      type="button"
      role="treeitem"
      aria-selected={isSelected}
      tabIndex={tabIndex}
      ref={(el) => ctx.registerItem(node.path, el)}
      onClick={() => ctx.onSelect(node.path)}
      onFocus={() => ctx.onFocusItem(node.path)}
      className={cn(
        "flex w-full items-center gap-1.5 py-1 text-left text-xs rounded-sm",
        isSelected
          ? "bg-accent text-accent-foreground"
          : "hover:bg-accent/50",
      )}
      style={{ paddingLeft: `${depth * 12 + 8 + 16}px` }}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="truncate">{node.name}</span>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Public component
// ---------------------------------------------------------------------------

export function FileTree({
  filePaths,
  selectedPath,
  onSelect,
}: {
  filePaths: string[];
  selectedPath: string;
  onSelect: (path: string) => void;
}) {
  const { t } = useT("skills");
  const tree = useMemo(() => buildTree(filePaths), [filePaths]);
  // Directories start expanded; the set tracks user-collapsed ones.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [focusedPath, setFocusedPath] = useState<string | null>(null);
  const itemRefs = useRef(new Map<string, HTMLButtonElement>());

  // Visible items in document order, with parent path for ArrowLeft.
  const visible = useMemo(() => {
    const out: { node: FileTreeNode; parent: string | null }[] = [];
    const walk = (nodes: FileTreeNode[], parent: string | null) => {
      for (const n of nodes) {
        out.push({ node: n, parent });
        if (n.isDirectory && !collapsed.has(n.path)) walk(n.children, n.path);
      }
    };
    walk(tree, null);
    return out;
  }, [tree, collapsed]);

  // The single tabbable item: last focused if still visible, else selection,
  // else the first item.
  const focusPath =
    (focusedPath && visible.some((v) => v.node.path === focusedPath)
      ? focusedPath
      : null) ??
    (visible.some((v) => v.node.path === selectedPath)
      ? selectedPath
      : visible[0]?.node.path ?? "");

  const toggleDir = (path: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const focusItem = (path: string) => {
    setFocusedPath(path);
    itemRefs.current.get(path)?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    const idx = visible.findIndex((v) => v.node.path === focusPath);
    if (idx < 0) return;
    const { node, parent } = visible[idx]!;
    switch (e.key) {
      case "ArrowDown":
        if (idx + 1 < visible.length) focusItem(visible[idx + 1]!.node.path);
        break;
      case "ArrowUp":
        if (idx > 0) focusItem(visible[idx - 1]!.node.path);
        break;
      case "ArrowRight":
        if (node.isDirectory) {
          if (collapsed.has(node.path)) toggleDir(node.path);
          else if (node.children.length > 0)
            focusItem(node.children[0]!.path);
        }
        break;
      case "ArrowLeft":
        if (node.isDirectory && !collapsed.has(node.path))
          toggleDir(node.path);
        else if (parent) focusItem(parent);
        break;
      case "Home":
        if (visible.length > 0) focusItem(visible[0]!.node.path);
        break;
      case "End":
        if (visible.length > 0) focusItem(visible[visible.length - 1]!.node.path);
        break;
      default:
        return;
    }
    e.preventDefault();
  };

  if (tree.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
        <FolderOpen className="h-5 w-5 text-muted-foreground/40" />
        <p className="mt-2 text-xs">{t(($) => $.file_tree.no_files)}</p>
      </div>
    );
  }

  const ctx: TreeItemContext = {
    selectedPath,
    focusPath,
    collapsed,
    onSelect,
    onToggleDir: toggleDir,
    onFocusItem: setFocusedPath,
    registerItem: (path, el) => {
      if (el) itemRefs.current.set(path, el);
      else itemRefs.current.delete(path);
    },
  };

  return (
    <div
      role="tree"
      aria-label={t(($) => $.file_tree.aria_label)}
      onKeyDown={handleKeyDown}
      className="py-1 px-1"
    >
      {tree.map((node) => (
        <TreeNodeItem key={node.path} node={node} ctx={ctx} />
      ))}
    </div>
  );
}
