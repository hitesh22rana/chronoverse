import { Brand } from "@/components/brand";
import { SearchTrigger } from "@/components/docs/search-dialog";
import { GitHubMark } from "@/components/github-mark";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { REPOSITORY_URL } from "@/lib/site";

export function SiteHeader() {
  return (
    <header className="site-header">
      <div className="site-header-inner">
        <Brand eager />
        <div className="header-actions">
          <SearchTrigger compact />
          <ThemeToggle />
          <Button asChild className="github-link" size="icon" variant="ghost">
            <a href={REPOSITORY_URL} target="_blank" rel="noopener noreferrer" aria-label="Open GitHub repository">
              <GitHubMark data-icon="inline-start" />
              <span className="sr-only">GitHub repository</span>
            </a>
          </Button>
        </div>
      </div>
    </header>
  );
}
