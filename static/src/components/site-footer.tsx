import Link from "next/link";

import { Brand } from "@/components/brand";
import { AUTHOR_PROFILES, CONTACT_EMAIL, REPOSITORY_URL } from "@/lib/site";

const lastUpdated = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" }).format(new Date());

export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="site-footer-inner">
        <div><Brand /><p>Distributed scheduling and orchestration for infrastructure you control.</p></div>
        <nav aria-label="Footer navigation">
          <Link href="/docs">Documentation</Link>
          <Link href="/docs/engineering/architecture">Engineering</Link>
          <Link href="/about">About</Link>
          <a href={`${REPOSITORY_URL}/blob/main/LICENSE`}>MIT License</a>
        </nav>
        <nav aria-label="Author profiles">
          {AUTHOR_PROFILES.map((profile) => <a key={profile.name} href={profile.url} rel="me noopener" target="_blank">{profile.name}</a>)}
          <a href={`mailto:${CONTACT_EMAIL}`}>Email</a>
        </nav>
        <p className="copyright">© {new Date().getFullYear()} Chronoverse · Last updated {lastUpdated}</p>
      </div>
    </footer>
  );
}
