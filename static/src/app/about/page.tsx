import type { Metadata } from "next";
import Link from "next/link";
import { ShieldCheck } from "lucide-react";

import { JsonLd } from "@/components/seo/json-ld";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getOpenApiOperations } from "@/lib/openapi";
import {
  AUTHOR_NAME,
  AUTHOR_PROFILES,
  CONTACT_EMAIL,
  REPOSITORY_URL,
  SITE_PUBLISHED,
  SITE_URL,
  sitePageUrl,
} from "@/lib/site";
import { docPages, docsConfig } from "../../../docs.config";

const description = "Who builds Chronoverse, what it runs on, how it is documented, and how to reach the maintainer.";
const lastUpdated = new Date();
const lastUpdatedLabel = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" }).format(lastUpdated);

export const metadata: Metadata = {
  title: "About",
  description,
  alternates: { canonical: sitePageUrl("/about") },
  openGraph: { title: "About Chronoverse", description, url: sitePageUrl("/about"), siteName: "Chronoverse", locale: "en_US" },
};

const structuredData = {
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "AboutPage",
      name: "About Chronoverse",
      description,
      url: sitePageUrl("/about"),
      datePublished: SITE_PUBLISHED,
      dateModified: lastUpdated.toISOString(),
      mainEntity: { "@id": `${SITE_URL}/#organization` },
    },
    {
      "@type": "BreadcrumbList",
      itemListElement: [
        { "@type": "ListItem", position: 1, name: "Chronoverse", item: sitePageUrl() },
        { "@type": "ListItem", position: 2, name: "About", item: sitePageUrl("/about") },
      ],
    },
  ],
};

export default function AboutPage() {
  const operationCount = getOpenApiOperations().length;

  return (
    <main>
      <JsonLd data={structuredData} />
      <section className="section-shell landing-section">
        <div className="section-heading">
          <Badge variant="secondary">About</Badge>
          <h2>Who builds Chronoverse.</h2>
          <p>Chronoverse is a self-hosted scheduler for heartbeat checks and container workloads. It is written in Go and TypeScript, and it is built, documented and maintained by {AUTHOR_NAME} in the open.</p>
        </div>

        <div className="engineering-split">
          <div>
            <div className="eyebrow"><ShieldCheck /> Maintainer</div>
            <h3>One author, one public repository.</h3>
            <p>The source, the docs and this site all live in the <a href={REPOSITORY_URL}>Chronoverse repository</a>. Every claim on these pages comes from that repository.</p>
            <div className="hero-actions">
              {AUTHOR_PROFILES.map((profile) => (
                <Button asChild key={profile.name} variant="outline"><a href={profile.url} rel="me noopener" target="_blank">{profile.name}</a></Button>
              ))}
            </div>
          </div>
          <div className="reliability-list">
            <div><span><strong>Stack</strong><p>Go and TypeScript services, Kafka workers, PostgreSQL, Redis, ClickHouse, Meilisearch, Docker and Kubernetes.</p></span></div>
            <div><span><strong>Observability</strong><p>OpenTelemetry traces, metrics and logs exported to the bundled Grafana OTEL LGTM stack.</p></span></div>
            <div><span><strong>Licence</strong><p>MIT. You run it on your own machines and you keep the data.</p></span></div>
            <div><span><strong>Support</strong><p>Open an issue on the repository, or email <a href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.</p></span></div>
          </div>
        </div>

        <div className="infra-grid">
          <Card><CardHeader><CardTitle>5</CardTitle></CardHeader><CardContent><CardDescription>gRPC domains behind the public API.</CardDescription></CardContent></Card>
          <Card><CardHeader><CardTitle>6</CardTitle></CardHeader><CardContent><CardDescription>worker roles that run the schedule.</CardDescription></CardContent></Card>
          <Card><CardHeader><CardTitle>4</CardTitle></CardHeader><CardContent><CardDescription>Kafka topics carry events and logs.</CardDescription></CardContent></Card>
          <Card><CardHeader><CardTitle>{docPages.length}</CardTitle></CardHeader><CardContent><CardDescription>guides across {docsConfig.length} documentation groups.</CardDescription></CardContent></Card>
        </div>

        <article className="docs-prose">
          <h2>What is Chronoverse?</h2>
          <p>Chronoverse runs scheduled and manual workflows across a Docker-backed execution fleet. It gives you 8 capabilities, from interval-based and on-demand runs through to live logs, retained search, analytics and notifications. The design assumes at-least-once delivery: messages repeat, processes restart, and ownership expires. Correctness comes from idempotency keys, a transactional outbox, workflow generations, deterministic event keys, durable leases and partition-aware Kafka commits.</p>

          <h2>What does it run on?</h2>
          <p>Go services and workers sit behind an HTTP and gRPC API. Kafka carries domain events and retained logs. PostgreSQL holds transactional state, idempotency records, outbox rows and leases. Redis holds sessions, cached reads and live log delivery. ClickHouse retains logs and Meilisearch indexes them for search. Containers execute on runtime nodes that register with the platform, and Kubernetes is the supported production orchestrator.</p>

          <h2>How is it documented?</h2>
          <p>The reference is authored as MDX in the repository: {docPages.length} guides across {docsConfig.length} groups, plus {operationCount} HTTP API operations generated from the OpenAPI contract at <code>static/content/openapi.yaml</code>. Navigation, link validation, search and this site all come out of the same <code>npm run build</code>.</p>

          <h2>Who can use it?</h2>
          <p>Anyone. Chronoverse is released under the <a href={`${REPOSITORY_URL}/blob/main/LICENSE`}>MIT licence</a>. You run it on your own machines, and you keep the data.</p>

          <h2>How do I get in touch?</h2>
          <p>Open an issue on the <a href={REPOSITORY_URL}>repository</a> for bugs and features, or email <a href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.</p>

          <p>Last updated {lastUpdatedLabel}.</p>
        </article>

        <div className="hero-actions">
          <Button asChild size="lg"><Link href="/docs">Read the docs</Link></Button>
          <Button asChild size="lg" variant="outline"><a href={REPOSITORY_URL} rel="noopener" target="_blank">View source</a></Button>
        </div>
      </section>
    </main>
  );
}
