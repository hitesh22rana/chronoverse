"use client"

import { useFormContext, useWatch } from "react-hook-form"
import { Plus, Trash2 } from "lucide-react"
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { ContainerListField, WorkflowNumberField } from "./workflow-form-fields"
import type { HeaderFormValue, WorkflowConfigurationValues } from "./workflow-form-values"

export function WorkflowConfigurationFields({ kind }: { kind: string }) {
    return (
        <Card>
            <CardHeader><CardTitle>{kind === "HEARTBEAT" ? "HTTP request" : "Container execution"}</CardTitle></CardHeader>
            <CardContent className="flex flex-col gap-4">
                {kind === "HEARTBEAT" ? <HeartbeatFields /> : <ContainerFields />}
            </CardContent>
        </Card>
    )
}

function HeartbeatFields() {
    const form = useFormContext<WorkflowConfigurationValues>()
    return (
        <>
            <FormField
                control={form.control}
                name="heartbeatPayload.endpoint"
                render={({ field }) => (
                    <FormItem>
                        <FormLabel>Endpoint URL</FormLabel>
                        <FormControl>
                            <Input
                                placeholder="https://example.com/api/health"
                                {...field}
                                value={field.value || ""}
                            />
                        </FormControl>
                        <FormDescription>
                            The URL to send the heartbeat request to
                        </FormDescription>
                        <FormMessage />
                    </FormItem>
                )}
            />

            <WorkflowNumberField name="heartbeatPayload.expectedStatusCode" />

            <HeartbeatHeaders />

            <FormField
                control={form.control}
                name="heartbeatPayload.timeout"
                render={({ field }) => (
                    <FormItem>
                        <FormLabel>Timeout (optional)</FormLabel>
                        <FormControl>
                            <Input
                                placeholder="10s"
                                {...field}
                                value={field.value || ""}
                            />
                        </FormControl>
                        <FormDescription>
                            Request timeout (e.g., &apos;30s&apos;, &apos;1m&apos;), max up to 5 minutes
                        </FormDescription>
                        <FormMessage />
                    </FormItem>
                )}
            />
        </>
    )
}

function HeartbeatHeaders() {
    const form = useFormContext<WorkflowConfigurationValues>()
    const headerFields = useWatch({ control: form.control, name: "heartbeatPayload.headers" }) ?? []
    return (
        <>
            <div className="flex flex-col gap-2">
                <FormLabel>
                    Headers (optional)
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="ml-2"
                        onClick={() => {
                            form.setValue("heartbeatPayload.headers", [
                                ...headerFields,
                                { id: crypto.randomUUID(), key: "", value: "" }
                            ])
                        }}
                    >
                        <Plus className="mr-1 h-3 w-3" /> Add header
                    </Button>
                </FormLabel>
                <FormDescription>
                    Optional HTTP headers to include with the request
                </FormDescription>

                {headerFields.map((header: HeaderFormValue, index: number) => (
                    <div key={header.id} className="flex items-center gap-2 mt-2">
                        <FormField
                            control={form.control}
                            name={`heartbeatPayload.headers.${index}.key`}
                            render={({ field }) => (
                                <FormItem className="flex-1">
                                    <FormLabel className="sr-only">Header {index + 1} name</FormLabel>
                                    <FormControl>
                                        <Input
                                            placeholder="Header name"
                                            {...field}
                                            value={field.value || ""}
                                        />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <FormField
                            control={form.control}
                            name={`heartbeatPayload.headers.${index}.value`}
                            render={({ field }) => (
                                <FormItem className="flex-1">
                                    <FormLabel className="sr-only">Header {index + 1} value</FormLabel>
                                    <FormControl>
                                        <Input
                                            placeholder="Value"
                                            {...field}
                                            value={field.value || ""}
                                        />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                                const updatedHeaders = [...headerFields]
                                updatedHeaders.splice(index, 1)
                                form.setValue("heartbeatPayload.headers", updatedHeaders)
                            }}
                        >
                            <Trash2 className="h-4 w-4" />
                            <span className="sr-only">Remove header {index + 1}</span>
                        </Button>
                    </div>
                ))}
            </div>
        </>
    )
}

function ContainerFields() {
    const form = useFormContext<WorkflowConfigurationValues>()
    const [cmdFields = [], cmdFieldIds = [], envFields = [], envFieldIds = []] = useWatch({
        control: form.control,
        name: ["containerPayload.cmd", "containerPayload.cmdIds", "containerPayload.env", "containerPayload.envIds"],
    })
    return (
        <>
            <FormField
                control={form.control}
                name="containerPayload.image"
                render={({ field }) => (
                    <FormItem>
                        <FormLabel>Image</FormLabel>
                        <FormControl>
                            <Input
                                placeholder="alpine:latest"
                                {...field}
                                value={field.value || ""}
                            />
                        </FormControl>
                        <FormDescription>
                            Docker image to run (e.g., alpine:latest)
                        </FormDescription>
                        <FormMessage />
                    </FormItem>
                )}
            />

            <ContainerListField name="cmd" values={cmdFields} ids={cmdFieldIds} />

            <ContainerListField name="env" values={envFields} ids={envFieldIds} />

            <FormField
                control={form.control}
                name="containerPayload.timeout"
                render={({ field }) => (
                    <FormItem>
                        <FormLabel>Timeout (optional)</FormLabel>
                        <FormControl>
                            <Input
                                placeholder="30s"
                                {...field}
                                value={field.value || ""}
                            />
                        </FormControl>
                        <FormDescription>
                            Maximum execution time (e.g., &quot;30s&quot;, &quot;5m&quot;), max up to 1 hour.
                        </FormDescription>
                        <FormMessage />
                    </FormItem>
                )}
            />
        </>
    )
}

