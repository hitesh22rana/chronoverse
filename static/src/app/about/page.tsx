import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Boxes, Layers, ScrollText, ShieldCheck, User } from "lucide-react";

import { JsonLd } from "@/components/seo/json-ld";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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

const description = "Who builds Chronoverse, what it runs on, and how to reach the maintainer.";
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

const profileNames = AUTHOR_PROFILES.reduce<ReactNode[]>((nodes, profile, index) => {
  if (index > 0) nodes.push(index === AUTHOR_PROFILES.length - 1 ? " and " : ", ");
  nodes.push(<a href={profile.url} key={profile.url} rel="me noopener" target="_blank">{profile.name}</a>);
  return nodes;
}, []);

const facts = [
  { icon: Boxes, label: "Runtime", text: "Docker containers run on runtime nodes that register with the platform. Kubernetes is the supported production orchestrator." },
  { icon: Layers, label: "Storage", text: "PostgreSQL holds transactional state, idempotency records, outbox rows and leases. ClickHouse retains logs, and Meilisearch indexes them." },
  { icon: ScrollText, label: "Observability", text: "Traces, metrics and logs export over OpenTelemetry to the bundled Grafana OTEL LGTM stack." },
  { icon: ShieldCheck, label: "Licence", text: "MIT. You run it on your own machines, and the data stays with you." },
];

export default function AboutPage() {
  const operationCount = getOpenApiOperations().length;

  return (
    <main>
      <JsonLd data={structuredData} />
      <section className="section-shell utility-section">
        <div className="section-heading">
          <Badge variant="secondary">About</Badge>
          <h2>Who builds Chronoverse.</h2>
          <p>{AUTHOR_NAME} writes the code, the documentation and this site. Chronoverse is a self-hosted scheduler for heartbeat checks and container workloads, written in Go and TypeScript.</p>
        </div>

        <div className="about-split">
          <div>
            <div className="eyebrow"><User /> Maintainer</div>
            <h2>One author, one repository.</h2>
            <p>The source, the docs and this site sit in the same repository, and the figures quoted across this site are read from it at build time.</p>
            <div className="about-links">
              {AUTHOR_PROFILES.map((profile) => (
                <Button asChild key={profile.name} variant="outline"><a href={profile.url} rel="me noopener" target="_blank">{profile.name}</a></Button>
              ))}
              <Button asChild variant="outline"><a href={`mailto:${CONTACT_EMAIL}`}>Email</a></Button>
            </div>
          </div>
          <ul className="about-facts">
            {facts.map(({ icon: Icon, label, text }) => (
              <li key={label}>
                <Icon aria-hidden="true" />
                <p>{text}</p>
              </li>
            ))}
          </ul>
        </div>

        <article className="docs-prose utility-prose">
          <h2>What it runs</h2>
          <p>Two workflow kinds. A HEARTBEAT workflow is a lightweight check that produces no logs. A CONTAINER workflow runs a Docker workload and can keep its stdout and stderr. Either can run on an interval or on demand, through the same replay-safe lifecycle.</p>

          <h2>How is it documented?</h2>
          <p>{docPages.length} guides across {docsConfig.length} groups, plus {operationCount} HTTP API operations generated from the OpenAPI contract at <code>static/content/openapi.yaml</code>. Navigation, link validation, search and this site all come out of the same <code>npm run build</code>.</p>

          <p>Report a defect as an issue on <a href={REPOSITORY_URL}>the repository</a>, find {AUTHOR_NAME} on {profileNames}, or <a href={`mailto:${CONTACT_EMAIL}`}>email</a>.</p>
        </article>
      </section>
    </main>
  );
}
