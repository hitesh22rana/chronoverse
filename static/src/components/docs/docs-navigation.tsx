import { Braces, Menu } from "lucide-react";
import Link from "next/link";

import { docsConfig } from "../../../docs.config";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { getOpenApiOperations } from "@/lib/openapi";

function NavigationContent({ activeSlug }: { activeSlug: string }) {
  const operations = getOpenApiOperations();
  return (
    <nav className="docs-navigation" aria-label="Documentation navigation">
      {docsConfig.map((group) => (
        <div className="docs-nav-group" key={group.title}>
          <h2>{group.title}</h2>
          <ul>
            {group.pages.map((page) => (
              <li key={page.slug}>
                <Link className={cn(activeSlug === page.slug && "active")} href={`/docs/${page.slug}`}>{page.title}</Link>
                {page.slug === "api/reference" && (
                  <ul className="docs-api-nav">
                    {operations.map((operation) => {
                      const slug = `api/reference/${operation.operationId}`;
                      return (
                        <li key={operation.operationId}>
                          <Link className={cn(activeSlug === slug && "active")} href={`/docs/${slug}`}>
                            <Braces />
                            <span>{operation.summary}</span>
                          </Link>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

export function DocsSidebar({ activeSlug }: { activeSlug: string }) {
  return <aside className="docs-sidebar"><NavigationContent activeSlug={activeSlug} /></aside>;
}

export function DocsMobileNavigation({ activeSlug }: { activeSlug: string }) {
  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button className="docs-mobile-trigger" variant="outline"><Menu data-icon="inline-start" />Browse docs</Button>
      </SheetTrigger>
      <SheetContent className="docs-mobile-sheet" side="left">
        <SheetHeader><SheetTitle>Documentation</SheetTitle></SheetHeader>
        <NavigationContent activeSlug={activeSlug} />
      </SheetContent>
    </Sheet>
  );
}
