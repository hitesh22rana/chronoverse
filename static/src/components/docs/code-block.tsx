"use client";

import { Check, Copy } from "lucide-react";
import { type HTMLAttributes, useRef, useState } from "react";

import { Button } from "@/components/ui/button";

export function CodeBlock(props: HTMLAttributes<HTMLPreElement>) {
  const ref = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);

  async function copy() {
    const text = ref.current?.innerText ?? "";
    await navigator.clipboard.writeText(text);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }

  return (
    <div className="code-block">
      <Button aria-label="Copy code" className="code-copy" onClick={copy} size="icon" type="button" variant="ghost">
        {copied ? <Check /> : <Copy />}
      </Button>
      <pre ref={ref} {...props} />
    </div>
  );
}
