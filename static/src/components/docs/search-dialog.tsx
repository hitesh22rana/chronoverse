"use client";

import Fuse from "fuse.js";
import { FileText, Search } from "lucide-react";
import Link from "next/link";
import { createContext, type ReactNode, useContext, useDeferredValue, useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { BASE_PATH } from "@/lib/site";
import { cn } from "@/lib/utils";

type SearchDocument = { title: string; description: string; headings: string; href: string; section: string; text: string };

const SearchContext = createContext<((open: boolean) => void) | null>(null);

export function SearchProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [documents, setDocuments] = useState<SearchDocument[]>([]);
  const deferredQuery = useDeferredValue(query.trim());

  useEffect(() => {
    function keydown(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen(true);
      }
    }
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, []);

  useEffect(() => {
    if (!open || documents.length > 0) return;
    fetch(`${BASE_PATH}/docs/search-index.json`)
      .then((response) => response.json())
      .then((value: SearchDocument[]) => setDocuments(value))
      .catch(() => setDocuments([]));
  }, [documents.length, open]);

  const fuse = useMemo(() => new Fuse(documents, {
    keys: [
      { name: "title", weight: 0.42 },
      { name: "headings", weight: 0.24 },
      { name: "description", weight: 0.16 },
      { name: "section", weight: 0.1 },
      { name: "text", weight: 0.08 },
    ],
    threshold: 0.36,
    ignoreLocation: true,
    includeScore: true,
    minMatchCharLength: 2,
  }), [documents]);
  const results = useMemo(() => {
    if (!deferredQuery) return documents.slice(0, 12);

    const normalizedQuery = deferredQuery.toLocaleLowerCase();
    return fuse.search(deferredQuery)
      .sort((left, right) => {
        const rank = (document: SearchDocument) => {
          const title = document.title.toLocaleLowerCase();
          if (title === normalizedQuery) return 0;
          if (title.startsWith(normalizedQuery)) return 1;
          if (title.includes(normalizedQuery)) return 2;
          if (document.headings.toLocaleLowerCase().includes(normalizedQuery)) return 3;
          return 4;
        };
        return rank(left.item) - rank(right.item) || (left.score ?? 1) - (right.score ?? 1);
      })
      .slice(0, 16)
      .map((result) => result.item);
  }, [deferredQuery, documents, fuse]);

  return (
    <SearchContext.Provider value={setOpen}>
      {children}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="search-dialog">
          <DialogHeader>
            <DialogTitle>Search documentation</DialogTitle>
            <DialogDescription>Search guides, engineering internals, operations, and API endpoints.</DialogDescription>
          </DialogHeader>
          <label className="search-input-wrap">
            <Search aria-hidden="true" />
            <span className="sr-only">Search query</span>
            <input autoFocus onChange={(event) => setQuery(event.target.value)} placeholder="Search Chronoverse..." value={query} />
          </label>
          <div className="search-results" role="list">
            {results.map((result) => (
              <Link href={result.href} key={result.href} onClick={() => setOpen(false)} role="listitem">
                <FileText />
                <span><strong>{result.title}</strong><small>{result.section} · {result.description}</small></span>
              </Link>
            ))}
            {query && results.length === 0 && <p>No documentation matched “{query}”.</p>}
          </div>
        </DialogContent>
      </Dialog>
    </SearchContext.Provider>
  );
}

export function SearchTrigger({ compact = false }: { compact?: boolean }) {
  const setOpen = useContext(SearchContext);

  if (!setOpen) throw new Error("SearchTrigger must be rendered inside SearchProvider");

  return (
    <Button
      className={cn("search-trigger", compact && "search-trigger-compact")}
      onClick={() => setOpen(true)}
      type="button"
      variant="outline"
      aria-label="Search documentation"
    >
      <Search data-icon="inline-start" />
      <span className="search-trigger-label">{compact ? "Search" : "Search docs"}</span>
      <kbd>⌘K</kbd>
    </Button>
  );
}
