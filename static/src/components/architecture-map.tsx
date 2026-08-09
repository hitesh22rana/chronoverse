import { ArrowRight, Container, Database, RadioTower, Server, Workflow } from "lucide-react";

import { architectureMapContent } from "@/lib/architecture";

export function ArchitectureMap() {
  const [gateway, domains, workers, runtime, infrastructure] = architectureMapContent.stages;

  return (
    <figure className="architecture-map" aria-labelledby="architecture-caption">
      <div className="architecture-flow">
        <div className="architecture-lane architecture-entry">
          <span className="architecture-icon"><Server /></span>
          <div><strong>{gateway.label}</strong><small>{gateway.items.join(" · ")}</small></div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column">
          <div className="architecture-label"><Workflow /> {domains.label}</div>
          <div className="architecture-chips">{domains.items.map((service) => <span key={service}>{service}</span>)}</div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column architecture-bus">
          <div className="architecture-label"><RadioTower /> {workers.label}</div>
          <div className="architecture-chips">{workers.items.map((worker) => <span key={worker}>{worker}</span>)}</div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column">
          <div className="architecture-label"><Container /> {runtime.label}</div>
          <div className="architecture-chips">{runtime.items.map((item) => <span key={item}>{item}</span>)}</div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column">
          <div className="architecture-label"><Database /> {infrastructure.label}</div>
          <div className="architecture-chips">{infrastructure.items.map((store) => <span key={store}>{store}</span>)}</div>
        </div>
      </div>
      <figcaption id="architecture-caption">{architectureMapContent.caption}</figcaption>
    </figure>
  );
}
