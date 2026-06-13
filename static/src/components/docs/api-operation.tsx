import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { OpenApiOperation } from "@/lib/openapi";

function formatSchema(value: unknown) {
  return JSON.stringify(value, null, 2);
}

export function ApiOperation({ operation }: { operation: OpenApiOperation }) {
  return (
    <div className="api-operation">
      <div className="api-path"><Badge variant="outline">{operation.method}</Badge><code>{operation.path}</code></div>
      {operation.description && <p>{operation.description}</p>}

      <h2 id="parameters">Parameters</h2>
      {operation.parameters.length > 0 ? (
        <div className="parameter-list">
          {operation.parameters.map((parameter) => (
            <Card key={`${parameter.in}-${parameter.name}`}>
              <CardHeader><CardTitle><code>{parameter.name}</code><Badge variant="secondary">{parameter.in}</Badge>{parameter.required && <Badge>required</Badge>}</CardTitle></CardHeader>
              <CardContent><p>{parameter.description ?? "No additional description."}</p><code>{parameter.schema?.type ?? "string"}</code></CardContent>
            </Card>
          ))}
        </div>
      ) : <p>This operation has no path, query, or header parameters.</p>}

      {operation.requestBody && <><h2 id="request-body">Request body</h2><div className="code-block"><pre><code>{formatSchema(operation.requestBody)}</code></pre></div></>}

      <h2 id="responses">Responses</h2>
      <div className="response-list">
        {Object.entries(operation.responses).map(([status, response]) => (
          <div key={status}><Badge variant="outline">{status}</Badge><p>{response.description ?? "Response"}</p></div>
        ))}
      </div>
    </div>
  );
}
