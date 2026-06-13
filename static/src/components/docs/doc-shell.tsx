import { ArrowLeft, ArrowRight, ChevronRight } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { DocsMobileNavigation, DocsSidebar } from "@/components/docs/docs-navigation";
import { TableOfContents } from "@/components/docs/table-of-contents";
import { Separator } from "@/components/ui/separator";
import type { DocHeading } from "@/lib/docs";

type NavigationPage = { slug: string; title: string } | undefined;

export function DocShell({
  activeSlug,
  children,
  description,
  headings,
  next,
  previous,
  title,
}: {
  activeSlug: string;
  children: ReactNode;
  description: string;
  headings: DocHeading[];
  next?: NavigationPage;
  previous?: NavigationPage;
  title: string;
}) {
  const section = activeSlug.split("/")[0].replaceAll("-", " ");
  return (
    <div className="docs-frame">
      <DocsSidebar activeSlug={activeSlug} />
      <main className="docs-main">
        <div className="docs-toolbar"><DocsMobileNavigation activeSlug={activeSlug} /></div>
        <div className="docs-breadcrumbs" aria-label="Breadcrumb">
          <Link href="/docs">Docs</Link><ChevronRight /><span>{section}</span>
        </div>
        <header className="docs-title">
          <h1>{title}</h1>
          <p>{description}</p>
        </header>
        <article className="docs-prose">{children}</article>
        <Separator />
        <nav className="docs-pagination" aria-label="Adjacent documentation">
          {previous ? <Link href={`/docs/${previous.slug}`}><ArrowLeft /><span><small>Previous</small>{previous.title}</span></Link> : <span />}
          {next ? <Link className="next" href={`/docs/${next.slug}`}><span><small>Next</small>{next.title}</span><ArrowRight /></Link> : <span />}
        </nav>
      </main>
      <TableOfContents headings={headings} />
    </div>
  );
}
