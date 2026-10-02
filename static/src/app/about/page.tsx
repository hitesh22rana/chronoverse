import type { Metadata } from "next";
import { Boxes, Layers, ScrollText, ShieldCheck } from "lucide-react";

import { JsonLd } from "@/components/seo/json-ld";
import { Badge } from "@/components/ui/badge";
import {
  AUTHOR_NAME,
  CONTACT_EMAIL,
  REPOSITORY_URL,
  SITE_PUBLISHED,
  SITE_URL,
  sitePageUrl,
} from "@/lib/site";

const description = "What Chronoverse is, what it runs on, who maintains it, and how to reach them.";
const lastUpdated = new Date();

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
      mainEntity: { "@type": "Organization", "@id": `${SITE_URL}/#organization` },
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

const facts = [
  { icon: Boxes, label: "Runtime", text: "Docker containers run on runtime nodes that register with the platform. Kubernetes is the supported production orchestrator." },
  { icon: Layers, label: "Storage", text: "PostgreSQL holds transactional state, idempotency records, outbox rows and leases. ClickHouse retains logs, and Meilisearch indexes them." },
  { icon: ScrollText, label: "Observability", text: "Traces, metrics and logs export over OpenTelemetry to the bundled Grafana OTEL LGTM stack." },
  { icon: ShieldCheck, label: "Licence", text: "MIT. You run it on your own machines, and the data stays with you." },
];

export default function AboutPage() {
  return (
    <main>
      <JsonLd data={structuredData} />
      <section className="section-shell utility-section">
        <div className="section-heading">
          <Badge variant="secondary">About</Badge>
          <h2>What Chronoverse is.</h2>
          <p>It schedules heartbeat checks and container workloads on infrastructure you control. The scheduler, the dashboard, the documentation and this site are built and released from one repository.</p>
        </div>

        <dl className="about-meta">
          <dt>Maintainer</dt>
          <dd>{AUTHOR_NAME}</dd>
        </dl>

        <ul className="about-facts">
          {facts.map(({ icon: Icon, label, text }) => (
            <li key={label}>
              <Icon aria-hidden="true" />
              <p>{text}</p>
            </li>
          ))}
        </ul>

        <article className="docs-prose utility-prose">
          <h2>What it runs</h2>
          <p>Two workflow kinds. A HEARTBEAT workflow is a lightweight check that produces no logs. A CONTAINER workflow runs a Docker workload and can keep its stdout and stderr. Either can run on an interval or on demand, through the same replay-safe lifecycle.</p>

          <h2>How is it documented?</h2>
          <p>The guides, the API reference and the OpenAPI contract are authored as MDX in the same repository. Navigation, link validation, search and this site all come out of one build.</p>

          <p>Report a defect as an issue on <a href={REPOSITORY_URL}>the repository</a>, or <a href={`mailto:${CONTACT_EMAIL}`}>email</a> the maintainer.</p>
        </article>
      </section>
    </main>
  );
}
