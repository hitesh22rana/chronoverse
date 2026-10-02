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
      <section className="section-shell utility-section">
        <div className="section-heading">
          <Badge variant="secondary">Terms</Badge>
          <h2>Terms of use.</h2>
          <p>Short, because the licence already says most of it.</p>
        </div>

        <article className="docs-prose utility-prose">
          <h2>The licence</h2>
          <p>Chronoverse is MIT licensed. The authoritative text is at <a href={`${REPOSITORY_URL}/blob/main/LICENSE`}>LICENSE</a>, and it governs the software, the documentation and this site equally.</p>

          <h2>No warranty</h2>
          <p>The software comes as is, without warranty of any kind. You run a scheduler that manages your own workloads, so test changes before they reach work you care about.</p>

          <h2>Figures on these pages</h2>
          <p>A stated figure says where it was read from. Values are counted at build time and can change between releases, so check the commit before relying on one.</p>

          <p>Report a defect as an issue on <a href={REPOSITORY_URL}>the repository</a>, or <a href={`mailto:${CONTACT_EMAIL}`}>email</a> the maintainer. Last updated {lastUpdatedLabel}.</p>
        </article>
      </section>
    </main>
  );
}
