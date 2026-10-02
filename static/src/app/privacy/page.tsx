import type { Metadata } from "next";
import Link from "next/link";

import { JsonLd } from "@/components/seo/json-ld";
import { Badge } from "@/components/ui/badge";
import { CONTACT_EMAIL, SITE_PUBLISHED, sitePageUrl } from "@/lib/site";

const description = "What this documentation site collects, which is nothing, and how to reach the maintainer about it.";
const lastUpdated = new Date();
const lastUpdatedLabel = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" }).format(lastUpdated);

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
      <section className="section-shell landing-section">
        <div className="section-heading">
          <Badge variant="secondary">Privacy</Badge>
          <h2>What this site collects.</h2>
          <p>Nothing. This page sets out what that means in practice.</p>
        </div>

        <article className="docs-prose">
          <h2>This site is a static export</h2>
          <p>These pages are pre-rendered HTML served from GitHub Pages. There is no application server behind them, no database, and no account to create.</p>

          <h2>No analytics, no tracking</h2>
          <p>The site loads no analytics, advertising or tag-manager script. There is no third-party script on any page here.</p>

          <h2>One value in your browser storage</h2>
          <p>Your light or dark theme choice is stored in your browser&rsquo;s local storage under <code>theme</code>, so the site does not flash the wrong theme on your next visit. It never leaves your device, and clearing site data removes it.</p>

          <h2>Server logs</h2>
          <p>GitHub serves these files from its own infrastructure and keeps its own request logs. That handling is covered by the GitHub Terms of Service and Privacy Statement, not by this project.</p>

          <h2>The Chronoverse application is separate</h2>
          <p>The scheduler itself is software you run yourself. If you run it, its handling of your data is whatever you configured. Read <Link href="/docs/deployment/security">Certificates and security</Link> for what the defaults are.</p>

          <h2>Questions</h2>
          <p>Questions about anything on this page go to the maintainer: <a href={`mailto:${CONTACT_EMAIL}`}>email</a>.</p>

          <p>Last updated {lastUpdatedLabel}.</p>
        </article>
      </section>
    </main>
  );
}
