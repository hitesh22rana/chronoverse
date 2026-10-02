import Image from "next/image";
import Link from "next/link";

import { withBasePath } from "@/lib/site";

export function Brand({ eager = false }: { eager?: boolean }) {
  return (
    <Link className="brand" href="/" aria-label="Chronoverse home">
      <span className="brand-mark" aria-hidden="true">
        <Image src={withBasePath("/assets/chronoverse-mark.webp")} alt="" width={128} height={128} priority={eager} />
      </span>
      <span>chronoverse</span>
    </Link>
  );
}
