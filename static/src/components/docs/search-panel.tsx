"use client";

import Fuse from "fuse.js";
import { FileText, Search } from "lucide-react";
import Link from "next/link";
import { useDeferredValue, useMemo, useState } from "react";
import useSWR from "swr";

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { BASE_PATH } from "@/lib/site";

type SearchDocument = { title: string; description: string; headings: string; href: string; section: string; text: string };

async function fetchSearchDocuments(url: string): Promise<SearchDocument[]> {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Search index request failed: ${response.status}`);
  return response.json() as Promise<SearchDocument[]>;
}

export function SearchPanel({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
  const [query, setQuery] = useState("");
  const deferredQuery = useDeferredValue(query.trim());
  const { data: documents = [] } = useSWR<SearchDocument[]>(
    `${BASE_PATH}/docs/search-index.json`,
    fetchSearchDocuments,
    { revalidateOnFocus: false, shouldRetryOnError: false },
  );

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
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="search-dialog">
        <DialogHeader>
          <DialogTitle>Search documentation</DialogTitle>
          <DialogDescription>Search guides, engineering internals, operations, and API endpoints.</DialogDescription>
        </DialogHeader>
        <label className="search-input-wrap">
          <Search aria-hidden="true" />
          <span className="sr-only">Search query</span>
          <input onChange={(event) => setQuery(event.target.value)} placeholder="Search Chronoverse..." value={query} />
        </label>
        <ul className="search-results">
          {results.map((result) => (
            <li key={result.href}>
              <Link href={result.href} onClick={() => onOpenChange(false)}>
                <FileText />
                <span><strong>{result.title}</strong><small>{result.section} · {result.description}</small></span>
              </Link>
            </li>
          ))}
        </ul>
        {query && results.length === 0 && <p>No documentation matched “{query}”.</p>}
      </DialogContent>
    </Dialog>
  );
}