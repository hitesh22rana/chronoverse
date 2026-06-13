import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type {
  OpenApiContent,
  OpenApiOperation,
  OpenApiParameter,
  OpenApiSchema,
} from "@/lib/openapi";

function formatSchema(schema: OpenApiSchema) {
  return JSON.stringify(schema, null, 2);
}

function formatValue(value: unknown) {
  return typeof value === "string" ? value : JSON.stringify(value);
}

function getSchemaConstraints(schema?: OpenApiSchema) {
  if (!schema) return ["string"];

  const constraints: string[] = [];
  const type = schema.type ?? (schema.properties ? "object" : schema.items ? "array" : "any");
  constraints.push(schema.format ? `${type} · ${schema.format}` : type);
  if (schema.enum) constraints.push(`enum: ${schema.enum.map(formatValue).join(" | ")}`);
  if (schema.default !== undefined) constraints.push(`default: ${formatValue(schema.default)}`);
  if (schema.minimum !== undefined) constraints.push(`min: ${schema.minimum}`);
  if (schema.maximum !== undefined) constraints.push(`max: ${schema.maximum}`);
  if (schema.minLength !== undefined) constraints.push(`min length: ${schema.minLength}`);
  if (schema.maxLength !== undefined) constraints.push(`max length: ${schema.maxLength}`);
  if (schema.minItems !== undefined) constraints.push(`min items: ${schema.minItems}`);
  if (schema.maxItems !== undefined) constraints.push(`max items: ${schema.maxItems}`);
  if (schema.uniqueItems) constraints.push("unique items");
  return constraints;
}

function ParameterConstraints({ parameter }: { parameter: OpenApiParameter }) {
  return (
    <div className="schema-constraints" aria-label={`${parameter.name} constraints`}>
      {getSchemaConstraints(parameter.schema).map((constraint) => (
        <code key={constraint}>{constraint}</code>
      ))}
    </div>
  );
}

function SchemaBlock({ schema }: { schema: OpenApiSchema }) {
  return (
    <div className="code-block schema-block">
      <pre><code>{formatSchema(schema)}</code></pre>
    </div>
  );
}

function ContentSchemas({ content }: { content: OpenApiContent }) {
  return (
    <div className="content-schema-list">
      {Object.entries(content).map(([mediaType, media]) => (
        <section className="content-schema" key={mediaType}>
          <Badge variant="secondary">{mediaType}</Badge>
          {media.schema ? <SchemaBlock schema={media.schema} /> : <p>No response schema.</p>}
        </section>
      ))}
    </div>
  );
}

function Authentication({ operation }: { operation: OpenApiOperation }) {
  if (operation.security.length === 0) {
    return <p>No authentication required.</p>;
  }

  return (
    <div className="security-requirements">
      {operation.security.map((requirement, index) => (
        <section className="security-alternative" key={`${index}-${Object.keys(requirement).join("-")}`}>
          {operation.security.length > 1 && <p>Authentication option {index + 1}</p>}
          {Object.entries(requirement).map(([name, scopes]) => {
            const scheme = operation.securitySchemes[name];
            const location = scheme.in && scheme.name ? `${scheme.in}: ${scheme.name}` : scheme.scheme ?? scheme.type;
            return (
              <Card key={name}>
                <CardHeader>
                  <CardTitle>
                    <code>{name}</code>
                    <Badge variant="secondary">{scheme.type}</Badge>
                    <Badge>required</Badge>
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <p>{scheme.description ?? "Authentication credential required by this operation."}</p>
                  <div className="schema-constraints">
                    <code>{location}</code>
                    {scopes.length > 0 && <code>scopes: {scopes.join(" | ")}</code>}
                  </div>
                </CardContent>
              </Card>
            );
          })}
          {Object.keys(requirement).length > 1 && <p className="security-note">All credentials in this group are required.</p>}
        </section>
      ))}
      {operation.security.length > 1 && <p className="security-note">Use any one authentication option shown above.</p>}
    </div>
  );
}

export function ApiOperation({ operation }: { operation: OpenApiOperation }) {
  return (
    <div className="api-operation">
      <div className="api-path">
        <Badge variant="outline">{operation.method}</Badge>
        <code>{operation.path}</code>
      </div>
      {operation.description && <p>{operation.description}</p>}

      <h2 id="authentication">Authentication</h2>
      <Authentication operation={operation} />

      <h2 id="parameters">Parameters</h2>
      {operation.parameters.length > 0 ? (
        <div className="parameter-list">
          {operation.parameters.map((parameter) => (
            <Card key={`${parameter.in}-${parameter.name}`}>
              <CardHeader>
                <CardTitle>
                  <code>{parameter.name}</code>
                  <Badge variant="secondary">{parameter.in}</Badge>
                  {parameter.required && <Badge>required</Badge>}
                </CardTitle>
              </CardHeader>
              <CardContent>
                <p>{parameter.description ?? "No additional description."}</p>
                <ParameterConstraints parameter={parameter} />
              </CardContent>
            </Card>
          ))}
        </div>
      ) : (
        <p>This operation has no path, query, header, or cookie parameters.</p>
      )}

      {operation.requestBody && (
        <>
          <h2 id="request-body">Request body</h2>
          <div className="request-body-header">
            {operation.requestBody.description && <p>{operation.requestBody.description}</p>}
            {operation.requestBody.required && <Badge>required</Badge>}
          </div>
          {operation.requestBody.content ? (
            <ContentSchemas content={operation.requestBody.content} />
          ) : (
            <p>No request body schema.</p>
          )}
        </>
      )}

      <h2 id="responses">Responses</h2>
      <div className="response-list">
        {Object.entries(operation.responses).map(([status, response]) => (
          <section className="response-item" key={status}>
            <div className="response-heading">
              <Badge variant="outline">{status}</Badge>
              <p>{response.description ?? "Response"}</p>
            </div>
            {response.content && <ContentSchemas content={response.content} />}
          </section>
        ))}
      </div>
    </div>
  );
}
