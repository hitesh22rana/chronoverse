// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps, ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Notification } from "./types";
import { NotificationsDrawer } from "./notifications-drawer";

const state = vi.hoisted(() => ({
  notifications: [] as Notification[],
  isLoading: false,
  markAsRead: vi.fn(),
  fetchNextPage: vi.fn(),
  isFetchingNextPage: false,
  hasNextPage: false,
}));
vi.mock("./use-notifications", () => ({ useNotifications: () => state }));
vi.mock("next/link", () => ({
  default: ({
    prefetch,
    ...props
  }: ComponentProps<"a"> & { prefetch?: boolean }) => {
    void prefetch;
    return <a {...props} />;
  },
}));
// jsdom has no viewport geometry; render virtual rows explicitly.
vi.mock("react-virtuoso", () => ({
  Virtuoso: ({
    totalCount,
    itemContent,
    endReached,
    components,
  }: {
    totalCount: number;
    itemContent: (_index: number) => ReactNode;
    endReached: () => void;
    components: { Footer: () => ReactNode };
  }) => (
    <div>
      {Array.from({ length: totalCount }, (_, index) => (
        <div key={index}>{itemContent(index)}</div>
      ))}
      <button onClick={endReached}>Load next page</button>
      <div data-testid="footer">
        <components.Footer />
      </div>
    </div>
  ),
}));
const close = vi.fn();
const item = (
  id: string,
  overrides: Partial<Notification> = {},
): Notification => ({
  id,
  kind: "WEB_INFO",
  payload: JSON.stringify({
    title: id,
    message: "Finished 'job' safely",
    action_url: `/jobs/${id}`,
  }),
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
  ...overrides,
});
beforeEach(() => {
  vi.setSystemTime(new Date("2026-06-15T12:00:00Z"));
  vi.clearAllMocks();
  Object.assign(state, {
    notifications: [],
    isLoading: false,
    isFetchingNextPage: false,
    hasNextPage: false,
  });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});
const mount = () => render(<NotificationsDrawer open onClose={close} />);

it("renders empty and loading states and disables empty bulk reads", () => {
  const view = mount();
  expect(screen.getByText("You're all caught up!")).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Mark as read" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  state.isLoading = true;
  view.rerender(<NotificationsDrawer open onClose={close} />);
  expect(screen.queryByText("You're all caught up!")).toBeNull();
  expect(document.querySelectorAll('[data-slot="skeleton"]')).toHaveLength(30);
});
it("groups dates and safely highlights quotes without injecting HTML", () => {
  const yesterday = new Date();
  yesterday.setDate(yesterday.getDate() - 1);
  state.notifications = [
    item("today"),
    item("same day", { kind: "UNKNOWN" }),
    item("yesterday", {
      created_at: yesterday.toISOString(),
      kind: "WEB_WARN",
    }),
    item("old", {
      created_at: "2020-01-02T12:00:00Z",
      kind: "WEB_ERROR",
      read_at: "2020-01-02",
      payload: JSON.stringify({
        title: "old",
        message: "<img src=x> & 'a<b & \"quoted\"' tail",
        action_url: "https://evil.example",
      }),
    }),
    item("success", { kind: "WEB_SUCCESS" }),
  ];
  mount();
  expect(screen.getAllByText("Today")).toHaveLength(1);
  expect(screen.getByText("Yesterday")).toBeTruthy();
  expect(screen.getByText("Jan 2, 2020")).toBeTruthy();
  const old = screen.getByRole("link", { name: /old/ });
  expect(old.getAttribute("href")).toBe("/");
  expect(old.querySelector("img")).toBeNull();
  expect(old.querySelector(".font-semibold")?.textContent).toBe(
    'a<b & "quoted"',
  );
  expect(old.textContent).toContain("<img src=x> &");
});
it("toggles individual and all selections and reads only visible IDs", () => {
  state.notifications = [item("one"), item("two")];
  const view = mount();
  const toggleAll = () =>
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Select all notifications" }),
    );
  fireEvent.click(screen.getByRole("checkbox", { name: "Select one" }));
  expect(screen.getByText("1 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("checkbox", { name: "Select one" }));
  expect(screen.getByText("0 selected")).toBeTruthy();
  toggleAll();
  expect(screen.getByText("2 selected")).toBeTruthy();
  toggleAll();
  expect(screen.getByText("0 selected")).toBeTruthy();
  toggleAll();
  state.notifications = [item("two")];
  view.rerender(<NotificationsDrawer open onClose={close} />);
  expect(screen.getByText("1 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Mark as read" }));
  expect(state.markAsRead).toHaveBeenCalledWith(["two"]);
  expect(screen.getByText("0 selected")).toBeTruthy();
});
it("reads unread links and removes only the clicked selection", () => {
  state.notifications = [
    item("unread"),
    item("read", { read_at: new Date().toISOString() }),
  ];
  mount();
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select all notifications" }),
  );
  fireEvent.click(screen.getByRole("link", { name: /unread/ }));
  expect(state.markAsRead).toHaveBeenCalledWith(["unread"]);
  expect(screen.getByText("1 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("link", { name: /^read/ }));
  expect(state.markAsRead).toHaveBeenCalledTimes(1);
  expect(screen.getByText("0 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("link", { name: /^read/ }));
  expect(close).toHaveBeenCalledTimes(3);
});
it("clears selections when closing the sheet", () => {
  state.notifications = [item("one")];
  const view = mount();
  fireEvent.click(screen.getByRole("checkbox", { name: "Select one" }));
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  expect(close).toHaveBeenCalledTimes(1);
  view.rerender(<NotificationsDrawer open={false} onClose={close} />);
  view.rerender(<NotificationsDrawer open onClose={close} />);
  expect(screen.getByText("0 selected")).toBeTruthy();
});
it("requests pages only when available and idle and shows a pending footer", () => {
  state.notifications = [item("one")];
  const view = mount();
  fireEvent.click(screen.getByRole("button", { name: "Load next page" }));
  expect(state.fetchNextPage).not.toHaveBeenCalled();
  state.hasNextPage = true;
  view.rerender(<NotificationsDrawer open onClose={close} />);
  fireEvent.click(screen.getByRole("button", { name: "Load next page" }));
  expect(state.fetchNextPage).toHaveBeenCalledTimes(1);
  state.isFetchingNextPage = true;
  view.rerender(<NotificationsDrawer open onClose={close} />);
  expect(
    screen.getByTestId("footer").querySelector('[data-slot="skeleton"]'),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Load next page" }));
  expect(state.fetchNextPage).toHaveBeenCalledTimes(1);
});
