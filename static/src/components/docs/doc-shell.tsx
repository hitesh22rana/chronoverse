import { ArrowLeft, ArrowRight, ChevronRight } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { docsConfig } from "../../../docs.config";
import { DocsMobileNavigation, DocsSidebar } from "@/components/docs/docs-navigation";
import { JsonLd } from "@/components/seo/json-ld";
import { TableOfContents } from "@/components/docs/table-of-contents";
import { Separator } from "@/components/ui/separator";
import type { DocHeading } from "@/lib/docs";
import { sitePageUrl } from "@/lib/site";

type NavigationPage = { slug: string; title: string } | undefined;

export function DocShell({
  activeSlug,
  canonicalPath,
  children,
  description,
  headings,
  next,
  previous,
  title,
}: {
  activeSlug: string;
  canonicalPath?: string;
  children: ReactNode;
  description: string;
  headings: DocHeading[];
  next?: NavigationPage;
  previous?: NavigationPage;
  title: string;
}) {
  const group = docsConfig.find((candidate) => candidate.pages.some((page) => activeSlug === page.slug || activeSlug.startsWith(`${page.slug}/`)))?.title ?? "Documentation";
  const isDocsIndex = canonicalPath === "/docs";
  const url = sitePageUrl(canonicalPath ?? `/docs/${activeSlug}`);
  const docsUrl = sitePageUrl("/docs");
  const breadcrumbItems = [
    { "@type": "ListItem", position: 1, name: "Chronoverse", item: sitePageUrl() },
    { "@type": "ListItem", position: 2, name: "Docs", item: docsUrl },
    ...(!isDocsIndex ? [
      { "@type": "ListItem", position: 3, name: group },
      { "@type": "ListItem", position: 4, name: title, item: url },
    ] : []),
  ];
  const structuredData = {
    "@context": "https://schema.org",
    "@graph": [
      isDocsIndex
        ? { "@type": "CollectionPage", name: title, description, url }
        : {
            "@type": "TechArticle",
            headline: title,
            description,
            url,
            mainEntityOfPage: url,
            isPartOf: { "@type": "WebSite", name: "Chronoverse documentation", url: docsUrl },
          },
      { "@type": "BreadcrumbList", itemListElement: breadcrumbItems },
    ],
  };

  return (
    <div className="docs-frame">
      <JsonLd data={structuredData} />
      <DocsSidebar activeSlug={activeSlug} />
      <main className="docs-main">
        <div className="docs-toolbar"><DocsMobileNavigation activeSlug={activeSlug} /></div>
        <div className="docs-breadcrumbs" aria-label="Breadcrumb">
          <Link href="/docs">Docs</Link>
          {!isDocsIndex && <><ChevronRight /><span>{group}</span><ChevronRight /><span>{title}</span></>}
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
