import type { Metadata } from "next";
import { notFound } from "next/navigation";
import type { ComponentType } from "react";

import { ApiOperation } from "@/components/docs/api-operation";
import { DocShell } from "@/components/docs/doc-shell";
import { docPages, getDocPage } from "../../../../docs.config";
import { getDocHeadings, getDocNavigation, getPageMetadata, isGeneratedApiSlug } from "@/lib/docs";
import { getOpenApiOperation, getOpenApiOperations } from "@/lib/openapi";
import { SOCIAL_IMAGE_ALT, SOCIAL_IMAGE_PATH, sitePageUrl, withBasePath } from "@/lib/site";

type PageProps = { params: Promise<{ slug: string[] }> };
type DocModule = { default: ComponentType };

const docLoaders: Record<string, () => Promise<DocModule>> = {
  introduction: () => import("@docs/introduction.mdx"),
  quickstart: () => import("@docs/quickstart.mdx"),
  installation: () => import("@docs/installation.mdx"),
  "getting-started/first-workflow": () => import("@docs/getting-started/first-workflow.mdx"),
  "concepts/workflows": () => import("@docs/concepts/workflows.mdx"),
  "concepts/jobs": () => import("@docs/concepts/jobs.mdx"),
  "concepts/scheduling": () => import("@docs/concepts/scheduling.mdx"),
  "concepts/workers": () => import("@docs/concepts/workers.mdx"),
  "concepts/lifecycle-states": () => import("@docs/concepts/lifecycle-states.mdx"),
  "concepts/log-retention": () => import("@docs/concepts/log-retention.mdx"),
  "engineering/architecture": () => import("@docs/engineering/architecture.mdx"),
  "engineering/service-boundaries": () => import("@docs/engineering/service-boundaries.mdx"),
  "engineering/event-flows": () => import("@docs/engineering/event-flows.mdx"),
  "engineering/replay-safety": () => import("@docs/engineering/replay-safety.mdx"),
  "engineering/transactional-outbox": () => import("@docs/engineering/transactional-outbox.mdx"),
  "engineering/job-leases": () => import("@docs/engineering/job-leases.mdx"),
  "engineering/kafka-processing": () => import("@docs/engineering/kafka-processing.mdx"),
  "engineering/image-pull-coordination": () => import("@docs/engineering/image-pull-coordination.mdx"),
  "engineering/logging-search-pipeline": () => import("@docs/engineering/logging-search-pipeline.mdx"),
  "engineering/trace-propagation": () => import("@docs/engineering/trace-propagation.mdx"),
  "features/workflow-types": () => import("@docs/features/workflow-types.mdx"),
  "features/job-scheduling": () => import("@docs/features/job-scheduling.mdx"),
  "features/log-streaming": () => import("@docs/features/log-streaming.mdx"),
  "features/notifications": () => import("@docs/features/notifications.mdx"),
  "features/analytics": () => import("@docs/features/analytics.mdx"),
  "api/overview": () => import("@docs/api/overview.mdx"),
  "api/authentication": () => import("@docs/api/authentication.mdx"),
  "api/request-safety": () => import("@docs/api/request-safety.mdx"),
  "api/pagination-errors": () => import("@docs/api/pagination-errors.mdx"),
  "api/server-sent-events": () => import("@docs/api/server-sent-events.mdx"),
  "api/reference": () => import("@docs/api/reference.mdx"),
  "internal/grpc-services": () => import("@docs/internal/grpc-services.mdx"),
  "internal/kafka-events": () => import("@docs/internal/kafka-events.mdx"),
  "internal/data-stores": () => import("@docs/internal/data-stores.mdx"),
  "deployment/overview": () => import("@docs/deployment/overview.mdx"),
  "deployment/development": () => import("@docs/deployment/development.mdx"),
  "deployment/production": () => import("@docs/deployment/production.mdx"),
  "deployment/kubernetes": () => import("@docs/deployment/kubernetes.mdx"),
  "deployment/configuration": () => import("@docs/deployment/configuration.mdx"),
  "deployment/security": () => import("@docs/deployment/security.mdx"),
  "operations/monitoring": () => import("@docs/operations/monitoring.mdx"),
  "operations/observability": () => import("@docs/operations/observability.mdx"),
  "operations/scaling": () => import("@docs/operations/scaling.mdx"),
  "operations/recovery": () => import("@docs/operations/recovery.mdx"),
  "operations/troubleshooting": () => import("@docs/operations/troubleshooting.mdx"),
  "contributing/repository-layout": () => import("@docs/contributing/repository-layout.mdx"),
  "contributing/development": () => import("@docs/contributing/development.mdx"),
};

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
  const url = sitePageUrl(`/docs/${slug}`);
  return {
    title,
    description: page.description,
    alternates: { canonical: url },
    openGraph: { title, description: page.description, type: "article", url, images: [{ url: withBasePath(SOCIAL_IMAGE_PATH), width: 1200, height: 630, alt: SOCIAL_IMAGE_ALT }] },
    twitter: { card: "summary_large_image", title, description: page.description, images: [{ url: withBasePath(SOCIAL_IMAGE_PATH), alt: SOCIAL_IMAGE_ALT }] },
  };
}

export default async function DocumentationPage({ params }: PageProps) {
  const slug = (await params).slug.join("/");
  const navigation = getDocNavigation(slug);

  if (isGeneratedApiSlug(slug)) {
    const operation = getOpenApiOperation(slug.split("/").at(-1) ?? "");
    if (!operation) notFound();
    return (
      <DocShell activeSlug={slug} description={`${operation.method} ${operation.path}`} headings={[{ id: "authentication", title: "Authentication", level: 2 }, { id: "parameters", title: "Parameters", level: 2 }, ...(operation.requestBody ? [{ id: "request-body", title: "Request body", level: 2 } as const] : []), { id: "responses", title: "Responses", level: 2 }]} next={navigation.next} previous={navigation.previous} title={operation.summary}>
        <ApiOperation operation={operation} />
      </DocShell>
    );
  }

  const page = getDocPage(slug);
  if (!page) notFound();
  const loadContent = docLoaders[page.source];
  if (!loadContent) notFound();
  const Content = (await loadContent()).default;

  return (
    <DocShell activeSlug={slug} description={page.description} headings={getDocHeadings(page.source)} next={navigation.next} previous={navigation.previous} title={page.title}>
      <Content />
    </DocShell>
  );
}
