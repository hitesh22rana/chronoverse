import { AlertTriangle, ArrowUpRight, CircleAlert, Info, Terminal } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { ArchitectureMap as SharedArchitectureMap } from "@/components/architecture-map";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { REPOSITORY_URL } from "@/lib/site";

export function ArchitectureMap() {
  return <SharedArchitectureMap />;
}

export function Callout({ children, title, type = "info" }: { children: ReactNode; title: string; type?: "info" | "note" | "warning" }) {
  const Icon = type === "warning" ? AlertTriangle : type === "note" ? CircleAlert : Info;
  return (
    <Alert variant={type === "warning" ? "destructive" : "default"}>
      <Icon />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{children}</AlertDescription>
    </Alert>
  );
}

export function CardGrid({ children }: { children: ReactNode }) {
  return <div className="doc-card-grid">{children}</div>;
}

export function DocCard({ children, href, title }: { children: ReactNode; href: string; title: string }) {
  return (
    <Card className="doc-card">
      <CardHeader>
        <CardTitle><Link href={href}>{title}<ArrowUpRight /></Link></CardTitle>
        <CardDescription>{children}</CardDescription>
      </CardHeader>
    </Card>
  );
}

export function Steps({ children }: { children: ReactNode }) {
  return <ol className="steps">{children}</ol>;
}

export function Step({ children, title }: { children: ReactNode; title: string }) {
  return <li><div className="step-marker" aria-hidden="true" /><div><strong>{title}</strong><div>{children}</div></div></li>;
}

export function Endpoint({ children, method, path }: { children?: ReactNode; method: string; path: string }) {
  return <div className="endpoint"><Badge variant="outline">{method}</Badge><code>{path}</code>{children}</div>;
}

export function SourceLink({ children, path }: { children: ReactNode; path: string }) {
  return (
    <a className="source-link" href={`${REPOSITORY_URL}/blob/main/${path}`} target="_blank" rel="noreferrer">
      <Terminal />
      <span>{children}</span>
      <code>{path}</code>
      <ArrowUpRight />
    </a>
  );
}
