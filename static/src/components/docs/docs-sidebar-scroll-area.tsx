"use client";

import type { ReactNode, UIEvent } from "react";
import { useLayoutEffect, useRef } from "react";

const SCROLL_POSITION_KEY = "chronoverse:docs-sidebar-scroll";

function rememberScrollPosition(event: UIEvent<HTMLElement>) {
  sessionStorage.setItem(SCROLL_POSITION_KEY, String(event.currentTarget.scrollTop));
}

export function DocsSidebarScrollArea({ activeSlug, children }: { activeSlug: string; children: ReactNode }) {
  const sidebarRef = useRef<HTMLElement>(null);

  useLayoutEffect(() => {
    const sidebar = sidebarRef.current;
    if (!sidebar) return;

    const savedScrollTop = Number(sessionStorage.getItem(SCROLL_POSITION_KEY));
    if (Number.isFinite(savedScrollTop)) sidebar.scrollTop = savedScrollTop;

    const activeLink = sidebar.querySelector<HTMLElement>('[aria-current="page"]');
    if (!activeLink) return;

    const sidebarBounds = sidebar.getBoundingClientRect();
    const activeLinkBounds = activeLink.getBoundingClientRect();
    const activeLinkIsVisible = activeLinkBounds.top >= sidebarBounds.top && activeLinkBounds.bottom <= sidebarBounds.bottom;
    if (activeLinkIsVisible) return;

    const centeredScrollTop = sidebar.scrollTop
      + activeLinkBounds.top
      - sidebarBounds.top
      - (sidebar.clientHeight - activeLinkBounds.height) / 2;
    const behavior = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
    sidebar.scrollTo({ top: centeredScrollTop, behavior });
  }, [activeSlug]);

  return <aside className="docs-sidebar" onScroll={rememberScrollPosition} ref={sidebarRef}>{children}</aside>;
}
