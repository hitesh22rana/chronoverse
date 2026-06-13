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
    const updateActiveHeading = () => {
      const visibleHeading = headings.find((heading) => visible.has(heading.id));
      if (visibleHeading) {
        setActiveId(visibleHeading.id);
        return;
      }

      const latestHeading = elements.filter((element) => element.getBoundingClientRect().top <= 112).at(-1);
      setActiveId(latestHeading?.id ?? elements[0].id);
    };

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) visible.add(entry.target.id);
          else visible.delete(entry.target.id);
        });
        updateActiveHeading();
      },
      { rootMargin: "-96px 0px -68% 0px", threshold: [0, 1] },
    );

    elements.forEach((element) => observer.observe(element));
    updateActiveHeading();

    return () => observer.disconnect();
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
