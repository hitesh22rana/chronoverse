import type { Metadata } from "next";

import { JsonLd } from "@/components/seo/json-ld";
import { Badge } from "@/components/ui/badge";
import { CONTACT_EMAIL, REPOSITORY_URL, SITE_PUBLISHED, sitePageUrl } from "@/lib/site";

const description = "The terms that cover Chronoverse: the MIT licence, the documentation, and how problems are handled.";
const lastUpdated = new Date();
const lastUpdatedLabel = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" }).format(lastUpdated);

export const metadata: Metadata = {
  title: "Terms",
  description,
  alternates: { canonical: sitePageUrl("/terms") },
};

const structuredData = {
  "@context": "https://schema.org",
  "@type": "WebPage",
  name: "Terms",
  description,
  url: sitePageUrl("/terms"),
  datePublished: SITE_PUBLISHED,
  dateModified: lastUpdated.toISOString(),
  inLanguage: "en",
};

export default function TermsPage() {
  return (
    <main>
      <JsonLd data={structuredData} />
      <section className="section-shell landing-section">
        <div className="section-heading">
          <Badge variant="secondary">Terms</Badge>
          <h2>Terms of use.</h2>
          <p>Chronoverse is MIT licensed software. These notes cover the licence, these pages, and how problems are handled.</p>
        </div>

        <article className="docs-prose">
          <h2>The software</h2>
          <p>Chronoverse is released under the MIT licence. The licence text is the authoritative version, at <a href={`${REPOSITORY_URL}/blob/main/LICENSE`}>LICENSE</a>. In short: you may use, copy, modify, merge, publish, distribute, sublicense and sell the software, and the licence text carries the warranty disclaimer and the conditions on the copyright notice.</p>

          <h2>These pages</h2>
          <p>The documentation and this site are part of the same repository and carry the same licence. Where a page states a measured value, it states where that value was read from. Values come from the source at build time and can change between releases, so check the commit before relying on one.</p>

          <h2>No warranty</h2>
          <p>The software is provided as is, without warranty of any kind. Running a scheduler that manages your workloads is your responsibility, and you should test changes before they reach anything you care about.</p>

          <h2>Trademarks and attribution</h2>
          <p>The project name and logo identify this project. Please do not use them to imply that another product comes from this one.</p>

          <h2>Problems</h2>
          <p>Report a defect or request a change as an issue on <a href={REPOSITORY_URL}>the repository</a>. For anything else, email <a href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.</p>

          <p>Last updated {lastUpdatedLabel}.</p>
        </article>
      </section>
    </main>
  );
}
