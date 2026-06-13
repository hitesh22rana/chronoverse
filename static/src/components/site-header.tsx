"use client";

import { Menu } from "lucide-react";
import Link from "next/link";

import { Brand } from "@/components/brand";
import { SearchTrigger } from "@/components/docs/search-dialog";
import { GitHubMark } from "@/components/github-mark";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { REPOSITORY_URL } from "@/lib/site";

const links = [
  { href: "/#product", label: "Product" },
  { href: "/#engineering", label: "Engineering" },
  { href: "/docs", label: "Docs" },
];

export function SiteHeader() {
  return (
    <header className="site-header">
      <div className="site-header-inner">
        <Brand />
        <nav className="desktop-nav" aria-label="Primary navigation">
          {links.map((link) => (
            <Link href={link.href} key={link.href}>{link.label}</Link>
          ))}
        </nav>
        <div className="header-actions">
          <SearchTrigger compact />
          <ThemeToggle />
          <Button asChild className="github-link" size="icon" variant="ghost">
            <a href={REPOSITORY_URL} target="_blank" rel="noreferrer" aria-label="Open GitHub repository">
              <GitHubMark data-icon="inline-start" />
            </a>
          </Button>
          <Sheet>
            <SheetTrigger asChild>
              <Button className="mobile-menu-trigger" size="icon" variant="ghost" aria-label="Open navigation">
                <Menu />
              </Button>
            </SheetTrigger>
            <SheetContent side="right">
              <SheetHeader>
                <SheetTitle>Navigation</SheetTitle>
              </SheetHeader>
              <nav className="mobile-nav" aria-label="Mobile navigation">
                {links.map((link) => (
                  <SheetClose asChild key={link.href}>
                    <Link href={link.href}>{link.label}</Link>
                  </SheetClose>
                ))}
                <SheetClose asChild>
                  <a href={REPOSITORY_URL} target="_blank" rel="noreferrer">GitHub</a>
                </SheetClose>
              </nav>
            </SheetContent>
          </Sheet>
        </div>
      </div>
    </header>
  );
}
