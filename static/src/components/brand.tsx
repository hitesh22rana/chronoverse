import Link from "next/link";

import { withBasePath } from "@/lib/site";

export function Brand({ eager = false }: { eager?: boolean }) {
  const source = withBasePath("/assets/chronoverse-mark.webp");
  return (
    <Link className="brand" href="/" aria-label="Chronoverse home">
      <span className="brand-mark" aria-hidden="true">
        {/* eslint-disable-next-line @next/next/no-img-element -- hand-written srcset, because the static export disables the image optimiser */}
        <img
          src={source}
          srcSet={`${withBasePath("/assets/chronoverse-mark-32.webp")} 32w, ${withBasePath("/assets/chronoverse-mark-64.webp")} 64w, ${source} 128w`}
          sizes="32px"
          alt=""
          width={128}
          height={128}
          loading={eager ? "eager" : "lazy"}
          fetchPriority={eager ? "high" : "auto"}
          decoding="async"
        />
      </span>
      <span>chronoverse</span>
    </Link>
  );
}
