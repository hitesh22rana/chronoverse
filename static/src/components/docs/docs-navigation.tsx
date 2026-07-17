import { Braces, Menu } from "lucide-react";
import Link from "next/link";

import { docsConfig } from "../../../docs.config";
import { DocsSidebarScrollArea } from "@/components/docs/docs-sidebar-scroll-area";
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
                <Link
                  aria-current={activeSlug === page.slug ? "page" : undefined}
                  className={cn(activeSlug === page.slug && "active")}
                  href={`/docs/${page.slug}`}
                >
                  {page.title}
                </Link>
                {page.slug === "api/reference" && (
                  <ul className="docs-api-nav">
                    {operations.map((operation) => {
                      const slug = `api/reference/${operation.operationId}`;
                      return (
                        <li key={operation.operationId}>
                          <Link
                            aria-current={activeSlug === slug ? "page" : undefined}
                            className={cn(activeSlug === slug && "active")}
                            href={`/docs/${slug}`}
                          >
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
  return <DocsSidebarScrollArea activeSlug={activeSlug}><NavigationContent activeSlug={activeSlug} /></DocsSidebarScrollArea>;
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
