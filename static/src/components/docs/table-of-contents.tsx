"use client";

import { useEffect, useState } from "react";

import type { DocHeading } from "@/lib/docs";
import { cn } from "@/lib/utils";

export function TableOfContents({ headings }: { headings: DocHeading[] }) {
  const [activeId, setActiveId] = useState(headings[0]?.id ?? "");

  useEffect(() => {
    const elements = headings
      .map((heading) => document.getElementById(heading.id))
      .filter((element): element is HTMLElement => element !== null);

    if (elements.length === 0) return;

    const visible = new Set<string>();
    let frame = 0;

    const activateHeading = (id: string) => {
      setActiveId((current) => current === id ? current : id);
      const nextUrl = `${window.location.pathname}${window.location.search}#${encodeURIComponent(id)}`;
      if (`${window.location.pathname}${window.location.search}${window.location.hash}` !== nextUrl) {
        window.history.replaceState(window.history.state, "", nextUrl);
      }
    };

    const updateActiveHeading = () => {
      const documentBottom = document.documentElement.scrollHeight - window.innerHeight;
      if (window.scrollY >= documentBottom - 2) {
        activateHeading(elements.at(-1)?.id ?? elements[0].id);
        return;
      }

      const visibleHeading = headings.find((heading) => visible.has(heading.id));
      if (visibleHeading) {
        activateHeading(visibleHeading.id);
        return;
      }

      const latestHeading = elements.filter((element) => element.getBoundingClientRect().top <= 112).at(-1);
      activateHeading(latestHeading?.id ?? elements[0].id);
    };

    const scheduleUpdate = () => {
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        updateActiveHeading();
      });
    };

    const syncFromHash = () => {
      const hash = decodeURIComponent(window.location.hash.slice(1));
      if (elements.some((element) => element.id === hash)) setActiveId(hash);
    };

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) visible.add(entry.target.id);
          else visible.delete(entry.target.id);
        });
        scheduleUpdate();
      },
      { rootMargin: "-96px 0px -68% 0px", threshold: [0, 1] },
    );

    elements.forEach((element) => observer.observe(element));
    window.addEventListener("scroll", scheduleUpdate, { passive: true });
    window.addEventListener("resize", scheduleUpdate);
    window.addEventListener("hashchange", syncFromHash);
    syncFromHash();
    scheduleUpdate();

    return () => {
      observer.disconnect();
      window.removeEventListener("scroll", scheduleUpdate);
      window.removeEventListener("resize", scheduleUpdate);
      window.removeEventListener("hashchange", syncFromHash);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, [headings]);

  if (headings.length === 0) return null;
  return (
    <aside className="docs-toc" aria-label="On this page">
      <h2>On this page</h2>
      <ul>
        {headings.map((heading) => (
          <li key={heading.id}>
            <a aria-current={activeId === heading.id ? "location" : undefined} className={cn(heading.level === 3 && "nested", activeId === heading.id && "active")} href={`#${heading.id}`}>{heading.title}</a>
          </li>
        ))}
      </ul>
    </aside>
  );
}
