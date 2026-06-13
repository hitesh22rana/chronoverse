import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { ApiOperation } from "@/components/docs/api-operation";
import { DocShell } from "@/components/docs/doc-shell";
import { docPages, getDocPage } from "../../../../docs.config";
import { getDocHeadings, getDocNavigation, getPageMetadata, isGeneratedApiSlug } from "@/lib/docs";
import { getOpenApiOperation, getOpenApiOperations } from "@/lib/openapi";
import { SITE_URL } from "@/lib/site";

type PageProps = { params: Promise<{ slug: string[] }> };

export const dynamicParams = false;

export function generateStaticParams() {
  return [
    ...docPages.map((page) => ({ slug: page.slug.split("/") })),
    ...getOpenApiOperations().map((operation) => ({ slug: ["api", "reference", operation.operationId] })),
  ];
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const slug = (await params).slug.join("/");
  const page = getPageMetadata(slug);
  if (!page) return {};

  const title = `${page.title} | Docs`;
  const url = `${SITE_URL}/docs/${slug}/`;
  return {
    title,
    description: page.description,
    alternates: { canonical: url },
    openGraph: { title, description: page.description, type: "article", url },
    twitter: { card: "summary", title, description: page.description },
  };
}

export default async function DocumentationPage({ params }: PageProps) {
  const slug = (await params).slug.join("/");
  const navigation = getDocNavigation(slug);

  if (isGeneratedApiSlug(slug)) {
    const operation = getOpenApiOperation(slug.split("/").at(-1) ?? "");
    if (!operation) notFound();
    return (
      <DocShell activeSlug={slug} description={`${operation.method} ${operation.path}`} headings={[{ id: "parameters", title: "Parameters", level: 2 }, ...(operation.requestBody ? [{ id: "request-body", title: "Request body", level: 2 } as const] : []), { id: "responses", title: "Responses", level: 2 }]} next={navigation.next} previous={navigation.previous} title={operation.summary}>
        <ApiOperation operation={operation} />
      </DocShell>
    );
  }

  const page = getDocPage(slug);
  if (!page) notFound();
  const Content = (await import(`@docs/${page.source}.mdx`)).default;

  return (
    <DocShell activeSlug={slug} description={page.description} headings={getDocHeadings(page.source)} next={navigation.next} previous={navigation.previous} title={page.title}>
      <Content />
    </DocShell>
  );
}
