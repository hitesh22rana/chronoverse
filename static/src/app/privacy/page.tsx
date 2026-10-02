import type { Metadata } from "next";
import Link from "next/link";
import { JsonLd } from "@/components/seo/json-ld";
import { Badge } from "@/components/ui/badge";
import { CONTACT_EMAIL, SITE_PUBLISHED, sitePageUrl } from "@/lib/site";

const description = "What this documentation site collects, which is nothing, and how to reach the maintainer about it.";
const lastUpdated = new Date();

export const metadata: Metadata = {
  title: "Privacy",
  description,
  alternates: { canonical: sitePageUrl("/privacy") },
};

const structuredData = {
  "@context": "https://schema.org",
  "@type": "WebPage",
  name: "Privacy",
  description,
  url: sitePageUrl("/privacy"),
  datePublished: SITE_PUBLISHED,
  dateModified: lastUpdated.toISOString(),
  inLanguage: "en",
};

export default function PrivacyPage() {
  return (
    <main>
      <JsonLd data={structuredData} />
      <section className="section-shell utility-section">
        <div className="section-heading">
          <Badge variant="secondary">Privacy</Badge>
          <h2>What this site collects.</h2>
          <p>Nothing worth a policy. Here is what that means.</p>
        </div>

        <article className="docs-prose utility-prose">
          <h2>A static site with no scripts of its own</h2>
          <p>These pages are pre-rendered HTML from GitHub Pages. No server, no database, no account, no cookies. GitHub keeps its own request logs under the GitHub Terms of Service; this project never sees them.</p>

          <h2>No analytics</h2>
          <p>No analytics, advertising or tag manager loads on any page here.</p>

          <h2>One value in local storage</h2>
          <p>Your theme choice is kept in local storage under <code>theme</code> so the page does not flash the wrong colours. It stays on your device, and clearing site data removes it.</p>

          <h2>The scheduler is a separate thing</h2>
          <p>Chronoverse is software you run yourself. If you run it, its data handling is whatever you configured &mdash; see <Link href="/docs/deployment/security">Certificates and security</Link> for the defaults.</p>

          <p>Questions go to the maintainer: <a href={`mailto:${CONTACT_EMAIL}`}>email</a>.</p>
        </article>
      </section>
    </main>
  );
}
