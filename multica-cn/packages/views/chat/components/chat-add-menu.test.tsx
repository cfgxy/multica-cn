import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { ChatAddMenu } from "./chat-add-menu";

describe("ChatAddMenu", () => {
  it("inserts a selected quick reply through its caller without sending", () => {
    const onSelectQuickReply = vi.fn();
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { common: enCommon, chat: enChat } }}
      >
        <ChatAddMenu
          onSelectQuickReply={onSelectQuickReply}
          quickReplies={[
            {
              id: "reply-1",
              workspace_id: "ws-1",
              name: "Greeting",
              content: "Hello there",
              position: 0,
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
            },
          ]}
        />
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    fireEvent.click(screen.getByText("Insert a quick reply"));
    fireEvent.click(screen.getByText("Greeting"));

    expect(onSelectQuickReply).toHaveBeenCalledWith("Hello there");
  });
});
