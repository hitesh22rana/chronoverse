"use client";

import { Search } from "lucide-react";
import dynamic from "next/dynamic";
import { createContext, type ReactNode, useContext, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const SearchPanel = dynamic(() => import("./search-panel").then((module) => module.SearchPanel), { ssr: false });

const SearchContext = createContext<((open: boolean) => void) | null>(null);

export function SearchProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);

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

  return (
    <SearchContext.Provider value={setOpen}>
      {children}
      {open && <SearchPanel onOpenChange={setOpen} />}
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