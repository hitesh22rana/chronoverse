// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { queryKeys } from "@/lib/api/query-keys";
import { queryRefetchIntervals } from "@/lib/api/query-policy";
import type { User } from "@/features/users/types";
import { useNotifications } from "./use-notifications";

const mocks = vi.hoisted(() => ({
  user: undefined as User | undefined,
  fetchApi: vi.fn(),
  fetchApiJson: vi.fn(),
  error: vi.fn(),
}));
vi.mock("@/features/users/use-users", () => ({
  useUsers: () => ({ user: mocks.user }),
}));
vi.mock("@/lib/api/client", () => ({
  fetchApi: mocks.fetchApi,
  fetchApiJson: mocks.fetchApiJson,
}));
vi.mock("sonner", () => ({ toast: { error: mocks.error } }));
const clients: QueryClient[] = [];
const notification = (id: string) => ({
  id,
  kind: "WEB_INFO",
  payload: "{}",
  created_at: "2026-01-01",
  updated_at: "2026-01-01",
});
function mount(options?: { poll?: boolean }) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return {
    ...renderHook(() => useNotifications(options), { wrapper }),
    client,
  };
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.user = {
    email: "user@example.com",
    notification_preference: "ALL",
    created_at: "",
    updated_at: "",
  };
  mocks.fetchApi.mockResolvedValue(undefined);
  mocks.fetchApiJson.mockResolvedValue({ notifications: [] });
});
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
});

it("fetches and flattens cursor pages, preserving their order", async () => {
  mocks.fetchApiJson
    .mockResolvedValueOnce({
      notifications: [notification("first")],
      cursor: "a & b",
    })
    .mockResolvedValueOnce({ notifications: [notification("second")] });
  const { result } = mount();
  await waitFor(() =>
    expect(result.current.notifications.map((item) => item.id)).toEqual([
      "first",
    ]),
  );
  expect(result.current.hasNextPage).toBe(true);
  await act(async () => {
    await result.current.fetchNextPage();
  });
  await waitFor(() =>
    expect(result.current.notifications.map((item) => item.id)).toEqual([
      "first",
      "second",
    ]),
  );
  expect(mocks.fetchApiJson.mock.calls[0][0]).not.toContain("cursor");
  expect(mocks.fetchApiJson.mock.calls[1][0]).toContain("cursor=a+%26+b");
  expect(result.current.hasNextPage).toBe(false);
});

it.each([undefined, "NONE"])(
  "does not fetch for an unavailable or opted-out user (%s)",
  async (preference) => {
    mocks.user = preference
      ? { ...mocks.user!, notification_preference: preference as "NONE" }
      : undefined;
    const { result } = mount();
    expect(result.current.notifications).toEqual([]);
    expect(result.current.isLoading).toBe(false);
    expect(mocks.fetchApiJson).not.toHaveBeenCalled();
  },
);

it("configures optional foreground polling and exposes refetch", async () => {
  const { result, client } = mount({ poll: true });
  await waitFor(() => expect(mocks.fetchApiJson).toHaveBeenCalledTimes(1));
  const options = client
    .getQueryCache()
    .find({ queryKey: queryKeys.notifications })!.options;
  expect(options).toMatchObject({
    refetchInterval: queryRefetchIntervals.notifications,
    refetchIntervalInBackground: false,
  });
  await act(async () => {
    await result.current.refetch();
  });
  expect(mocks.fetchApiJson).toHaveBeenCalledTimes(2);
});

it("surfaces fetch failures without inventing notifications", async () => {
  mocks.fetchApiJson.mockRejectedValue(new Error("offline"));
  const { result } = mount();
  await waitFor(() => expect(result.current.error?.message).toBe("offline"));
  expect(result.current.notifications).toEqual([]);
  expect(mocks.error).toHaveBeenCalledWith("offline");
});

it("batches reads and removes matching IDs from every cached page", async () => {
  mocks.fetchApiJson
    .mockResolvedValueOnce({
      notifications: [notification("keep"), notification("read")],
      cursor: "next",
    })
    .mockResolvedValueOnce({
      notifications: [notification("read"), notification("last")],
    });
  const { result, client } = mount();
  await waitFor(() => expect(result.current.hasNextPage).toBe(true));
  await act(async () => {
    await result.current.fetchNextPage();
  });
  const ids = [
    "read",
    ...Array.from({ length: 100 }, (_, index) => `other-${index}`),
  ];
  act(() => result.current.markAsRead(ids));
  await waitFor(() =>
    expect(result.current.notifications.map((item) => item.id)).toEqual([
      "keep",
      "last",
    ]),
  );
  expect(mocks.fetchApi).toHaveBeenCalledTimes(2);
  expect(
    mocks.fetchApi.mock.calls.map(
      (call) => JSON.parse(call[2].body).ids.length,
    ),
  ).toEqual([100, 1]);
  expect(mocks.fetchApi.mock.calls[0][2].method).toBe("PUT");
  expect(client.getQueryData(queryKeys.notifications)).toMatchObject({
    pageParams: [null, "next"],
    pages: [{ cursor: "next" }, {}],
  });
});

it("keeps cached notifications when marking as read fails", async () => {
  mocks.fetchApiJson.mockResolvedValue({
    notifications: [notification("keep")],
  });
  mocks.fetchApi.mockRejectedValue(new Error("write failed"));
  const { result } = mount();
  await waitFor(() => expect(result.current.notifications).toHaveLength(1));
  act(() => result.current.markAsRead(["keep"]));
  await waitFor(() => expect(mocks.error).toHaveBeenCalledWith("write failed"));
  expect(result.current.notifications[0].id).toBe("keep");
});

it("allows read completion when no notification query is cached", async () => {
  mocks.user = undefined;
  const { result, client } = mount();
  act(() => result.current.markAsRead(["absent"]));
  await waitFor(() =>
    expect(client.getMutationCache().getAll()[0]?.state.status).toBe("success"),
  );
  expect(client.getQueryData(queryKeys.notifications)).toBeUndefined();
});
