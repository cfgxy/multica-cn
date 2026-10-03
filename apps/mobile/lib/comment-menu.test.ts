import { describe, expect, it } from "vitest";
import {
  buildCommentMenu,
  canEditCommentEntry,
} from "./comment-menu";

const LABELS = {
  reply: "Reply",
  react: "React…",
  edit: "Edit",
  copy: "Copy",
  select: "Select Text",
  copyLink: "Copy Link",
  resolve: "Resolve Thread",
  unresolve: "Unresolve Thread",
  delete: "Delete",
  cancel: "Cancel",
};

const baseInput = {
  hasContent: true,
  canCopyLink: false,
  isRoot: false,
  resolved: false,
  canEdit: false,
  isOwn: false,
  labels: LABELS,
};

describe("canEditCommentEntry", () => {
  it("allows the author to edit their own comment", () => {
    expect(
      canEditCommentEntry(
        { actor_type: "member", actor_id: "member-1" },
        "member-1",
        false,
      ),
    ).toBe(true);
  });

  it("forbids editing another member's comment without moderator role", () => {
    expect(
      canEditCommentEntry(
        { actor_type: "member", actor_id: "member-2" },
        "member-1",
        false,
      ),
    ).toBe(false);
  });

  it("allows a moderator to edit another member's comment (web parity)", () => {
    expect(
      canEditCommentEntry(
        { actor_type: "member", actor_id: "member-2" },
        "member-1",
        true,
      ),
    ).toBe(true);
  });

  it("forbids a moderator from editing an agent comment (web parity)", () => {
    expect(
      canEditCommentEntry(
        { actor_type: "agent", actor_id: "agent-1" },
        "member-1",
        true,
      ),
    ).toBe(false);
  });

  it("forbids editing when the current user is unknown", () => {
    expect(
      canEditCommentEntry(
        { actor_type: "member", actor_id: "member-1" },
        undefined,
        false,
      ),
    ).toBe(false);
  });
});

describe("buildCommentMenu", () => {
  it("places Edit before Delete for an editable own comment", () => {
    const menu = buildCommentMenu({ ...baseInput, canEdit: true, isOwn: true });
    const editIdx = menu.options.indexOf(LABELS.edit);
    const deleteIdx = menu.options.indexOf(LABELS.delete);
    expect(editIdx).toBeGreaterThanOrEqual(0);
    expect(deleteIdx).toBeGreaterThan(editIdx);
  });

  it("omits Edit when the entry is not editable — removing the edit-permission gate makes this assertion fail", () => {
    const menu = buildCommentMenu({
      ...baseInput,
      canEdit: false,
      isOwn: true,
    });
    expect(menu.options).not.toContain(LABELS.edit);
  });

  it("keeps Cancel last and marks Delete destructive only for own comments", () => {
    const own = buildCommentMenu({ ...baseInput, canEdit: true, isOwn: true });
    expect(menu_cancel(own)).toBe(true);
    expect(own.destructiveButtonIndex).toBe(
      own.options.indexOf(LABELS.delete),
    );

    const other = buildCommentMenu({ ...baseInput, isOwn: false });
    expect(other.destructiveButtonIndex).toBeUndefined();
  });

  it("omits Copy/Select Text when the comment has no content", () => {
    const menu = buildCommentMenu({ ...baseInput, hasContent: false });
    expect(menu.options).not.toContain(LABELS.copy);
    expect(menu.options).not.toContain(LABELS.select);
  });

  it("omits Copy Link when the issue link is unavailable", () => {
    const menu = buildCommentMenu({ ...baseInput, canCopyLink: false });
    expect(menu.options).not.toContain(LABELS.copyLink);
    const withLink = buildCommentMenu({ ...baseInput, canCopyLink: true });
    expect(withLink.options).toContain(LABELS.copyLink);
  });

  it("always ends with Cancel as a selectable action", () => {
    const menu = buildCommentMenu(baseInput);
    expect(menu.actions[menu.actions.length - 1]).toBe("cancel");
    expect(menu.cancelButtonIndex).toBe(menu.options.length - 1);
  });
});

function menu_cancel(menu: ReturnType<typeof buildCommentMenu>): boolean {
  return menu.cancelButtonIndex === menu.options.length - 1;
}
