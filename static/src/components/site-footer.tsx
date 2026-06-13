import Link from "next/link";

import { Brand } from "@/components/brand";
import { REPOSITORY_URL } from "@/lib/site";

export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="site-footer-inner">
        <div><Brand /><p>Distributed scheduling and orchestration for infrastructure you control.</p></div>
        <nav aria-label="Footer navigation"><Link href="/docs">Documentation</Link><Link href="/docs/engineering/architecture">Engineering</Link><a href={REPOSITORY_URL}>GitHub</a><a href={`${REPOSITORY_URL}/blob/main/LICENSE`}>MIT License</a></nav>
        <p className="copyright">© {new Date().getFullYear()} Chronoverse.</p>
      </div>
    </footer>
  );
}
