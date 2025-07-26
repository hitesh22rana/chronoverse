"use client"

import { Fragment, useEffect, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm } from "react-hook-form"
import { z } from "zod"
import { Duration, parseDuration } from '@alwatr/parse-duration'
import {
    Loader2,
    Plus,
    Trash2
} from "lucide-react"

import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle
} from "@/components/ui/dialog"
import {
    Form,
    FormControl,
    FormDescription,
    FormField,
    FormItem,
    FormLabel,
    FormMessage
} from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from "@/components/ui/select"
import {
    Card,
    CardContent,
    CardDescription,
    CardHeader,
    CardTitle
} from "@/components/ui/card"

import { useWorkflows } from "@/hooks/use-workflows"

// Base schema for common fields
const baseCreateWorkflowSchema = z.object({
    name: z.string().trim().min(3, "Name must be at least 3 characters").max(50, "Name must be at most 50 characters"),
    interval: z.union([
        z.string().trim().refine(val => val === "" || /^\d+$/.test(val), {
            message: "Please enter a valid number"
        }),
        z.number()
    ])
        .transform(val => val === "" ? undefined : Number(val))
        .refine(val => val === undefined || (val >= 1 && val <= 10080), {
            message: "Must be between 1 and 10080 minutes (1 week)"
        }),
    maxConsecutiveJobFailuresAllowed: z.coerce.number().int().min(3).max(100).default(3)
})

// Heartbeat payload schema
const heartbeatPayloadSchema = z.object({
    endpoint: z.url().trim().refine(val => val !== "", {
        message: "Please enter a valid URL"
    }),
    expectedStatusCode: z.coerce.number().int().min(100).max(599).default(200).refine(val => val >= 100 && val <= 599, {
        message: "Expected status code must be between 100 and 599"
    }),
    headers: z.array(
        z.object({
            key: z.string().trim().min(1, "Header key is required"),
            value: z.string().trim()
        })
    ).default([]),
    timeout: z.string().default("")
        .refine(val => {
            if (!val) return true
            try {
                const parsed = parseDuration(val as unknown as Duration, 's')
                return parsed > 0 && parsed <= 300
                // eslint-disable-next-line @typescript-eslint/no-unused-vars
            } catch (e) {
                return false
            }
        }, "Timeout must be a valid duration (e.g., '30s', '1m') max up to 5 minutes")
})

// Container payload schema
const containerPayloadSchema = z.object({
    image: z.string().trim().min(1, "Container image is required"),
    cmd: z.array(z.string().trim())
        .optional()
        .default([])
        .transform(val => val?.filter(item => item !== "") || []),
    env: z.array(z.string().trim())
        .optional()
        .default([])
        .transform(val => val?.filter(item => item !== "") || []),
    timeout: z.string().default("")
        .refine(val => {
            if (!val) return true
            try {
                const parsed = parseDuration(val as unknown as Duration, 's')
                return parsed > 0 && parsed <= 3600
                // eslint-disable-next-line @typescript-eslint/no-unused-vars
            } catch (e) {
                return false
            }
        }, "Timeout must be a valid duration (e.g., '30s', '5m') max up to 1 hour")
})

// Complete workflow schemas with discriminated union
const heartbeatWorkflowSchema = baseCreateWorkflowSchema.extend({
    kind: z.literal("HEARTBEAT"),
    heartbeatPayload: heartbeatPayloadSchema,
})

const containerWorkflowSchema = baseCreateWorkflowSchema.extend({
    kind: z.literal("CONTAINER"),
    containerPayload: containerPayloadSchema,
})

// Union the schemas to handle different kinds
const workflowSchema = z.discriminatedUnion("kind", [
    heartbeatWorkflowSchema,
    containerWorkflowSchema
])

type WorkflowFormValues = z.infer<typeof workflowSchema>

interface CreateWorkflowDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

const kindType = {
    'HEARTBEAT': {
        title: "Heartbeat workflow",
        description: "HEARTBEAT workflows are used to monitor the availability of external services. They make HTTP requests to specified endpoints at regular intervals.",
    },
    'CONTAINER': {
        title: "Container workflow",
        description: "Container workflows are used to run custom code in a containerized environment. They allow you to execute scripts or commands inside a specified container image.",
    }
}

type KindType = keyof typeof kindType

export function CreateWorkflowDialog({ open, onOpenChange }: CreateWorkflowDialogProps) {
    const { createWorkflow, isCreating } = useWorkflows()
    const [selectedKind, setSelectedKind] = useState<KindType>("HEARTBEAT")

    const form = useForm({
        resolver: zodResolver(workflowSchema),
        defaultValues: {
            name: "",
            kind: "HEARTBEAT",
            interval: 5,
            maxConsecutiveJobFailuresAllowed: 3,
            heartbeatPayload: {
                endpoint: "",
                expectedStatusCode: 200,
                headers: [],
                timeout: "",
            }
        },
        mode: "onChange",
    })

    // Watch kind changes and update form structure
    const watchedKind = form.watch("kind")

    useEffect(() => {
        if (watchedKind !== selectedKind) {
            setSelectedKind(watchedKind)

            // Reset the form with the appropriate structure
            if (watchedKind === "HEARTBEAT") {
                // For Heartbeat, clean container fields and set defaults
                form.setValue("heartbeatPayload", {
                    endpoint: "",
                    expectedStatusCode: 200,
                    headers: [],
                    timeout: ""
                })
                form.unregister("containerPayload")
            } else {
                // For Container, clean heartbeat fields and set defaults
                form.setValue("containerPayload", {
                    image: "",
                    cmd: [""],
                    env: [""],
                    timeout: ""
                })
                form.unregister("heartbeatPayload")
            }
        }
    }, [watchedKind, selectedKind, form])

    // Reset form when dialog opens
    useEffect(() => {
        if (open) {
            setSelectedKind("HEARTBEAT")
            form.reset({
                name: "",
                kind: "HEARTBEAT",
                interval: 5,
                maxConsecutiveJobFailuresAllowed: 3,
                heartbeatPayload: {
                    endpoint: "",
                    expectedStatusCode: 200,
                    headers: [],
                    timeout: ""
                }
            })
        }
    }, [open, form])

    const handleSubmit = (data: WorkflowFormValues) => {
        let payload: string = "{}"

        if (data.kind === "HEARTBEAT") {
            const { endpoint, expectedStatusCode, headers = [], timeout } = (data as z.infer<typeof heartbeatWorkflowSchema>).heartbeatPayload
            const headersObject = headers.reduce((acc, header) => {
                if (header.key) {
                    acc[header.key] = header.value
                }
                return acc
            }, {} as Record<string, string>)

            payload = JSON.stringify({
                endpoint,
                expected_status_code: expectedStatusCode,
                headers: headersObject,
                ...(timeout ? { timeout } : {})
            })
        } else if (data.kind === "CONTAINER") {
            const { image, cmd, env, timeout } = (data as z.infer<typeof containerWorkflowSchema>).containerPayload
            // parse env in key=value format and map to object
            const envObject = env.reduce((acc, item) => {
                const [key, value] = item.split("=")
                if (key) {
                    acc[key] = value || ""
                }
                return acc
            }, {} as Record<string, string>)

            payload = JSON.stringify({
                image,
                ...(cmd && cmd.length > 0 ? { cmd } : {}),
                ...(env && env.length > 0 ? { env: envObject } : {}),
                ...timeout ? { timeout } : {}
            })
        }

        // Submit the workflow with the constructed payload
        createWorkflow({
            name: data.name,
            kind: data.kind,
            payload: payload,
            interval: data.interval as number,
            max_consecutive_job_failures_allowed: data.maxConsecutiveJobFailuresAllowed
        })
        form.reset()
        onOpenChange(false)
    }

    // Get current field values based on selected kind
    const headerFields = selectedKind === "HEARTBEAT"
        ? form.watch("heartbeatPayload.headers") || []
        : []

    const cmdFields = selectedKind === "CONTAINER"
        ? form.watch("containerPayload.cmd") || []
        : []

    return (
        <Dialog
            open={open}
            onOpenChange={(newOpen) => {
                // Prevent closing if creating
                if (isCreating && !newOpen) return;
                onOpenChange(newOpen)
            }}
        >
            <DialogContent className="sm:max-w-2xl max-h-[95vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>Create new workflow</DialogTitle>
                    <DialogDescription>
                        Define a new workflow to be executed on a schedule.
                    </DialogDescription>
                </DialogHeader>

                <Form {...form}>
                    <form onSubmit={(e) => {
                        e.preventDefault();
                        form.handleSubmit((data) => {
                            handleSubmit(data);
                        })(e);
                    }} className="space-y-6 pt-2">
                        <FormField
                            control={form.control}
                            name="name"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>Name</FormLabel>
                                    <FormControl>
                                        <Input placeholder="My workflow" {...field} value={field.value || ""} />
                                    </FormControl>
                                    <FormDescription>
                                        A descriptive name for your workflow.
                                    </FormDescription>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <FormField
                            control={form.control}
                            name="kind"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>Workflow kind</FormLabel>
                                    <Select
                                        onValueChange={(value) => {
                                            field.onChange(value);
                                        }}
                                        value={field.value}
                                    >
                                        <FormControl>
                                            <SelectTrigger>
                                                <SelectValue placeholder="Select a workflow kind" />
                                            </SelectTrigger>
                                        </FormControl>
                                        <SelectContent>
                                            <SelectItem value="HEARTBEAT">
                                                <span>Heartbeat</span>
                                            </SelectItem>
                                            <SelectItem value="CONTAINER">
                                                <span>Container</span>
                                            </SelectItem>
                                        </SelectContent>
                                    </Select>
                                    <FormDescription>
                                        {kindType[selectedKind].description}
                                    </FormDescription>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <Card>
                            <CardHeader>
                                <CardTitle>{kindType[selectedKind].title} Configuration</CardTitle>
                                <CardDescription>
                                    Configure the parameters for your {selectedKind.toLowerCase()} workflow
                                </CardDescription>
                            </CardHeader>
                            <CardContent className="space-y-4">
                                {selectedKind === "HEARTBEAT" && (
                                    <Fragment>
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

                                        <FormField
                                            control={form.control}
                                            name="heartbeatPayload.expectedStatusCode"
                                            render={({ field }) => (
                                                <FormItem>
                                                    <FormLabel>Expected Status Code</FormLabel>
                                                    <FormControl>
                                                        <Input
                                                            type="number"
                                                            min={100}
                                                            max={599}
                                                            {...field}
                                                            value={field.value === undefined ? "" : field.value}
                                                            onChange={(e) => {
                                                                field.onChange(e.target.value === "" ? "" : Number(e.target.value));
                                                            }}
                                                        />
                                                    </FormControl>
                                                    <FormDescription>
                                                        The HTTP status code expected from the endpoint (default: 200)
                                                    </FormDescription>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                        <div className="space-y-2">
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
                                                            { key: "", value: "" }
                                                        ])
                                                    }}
                                                >
                                                    <Plus className="mr-1 h-3 w-3" /> Add header
                                                </Button>
                                            </FormLabel>
                                            <FormDescription>
                                                Optional HTTP headers to include with the request
                                            </FormDescription>

                                            {headerFields.map((_, index) => (
                                                <div key={index} className="flex items-center gap-2 mt-2">
                                                    <FormField
                                                        control={form.control}
                                                        name={`heartbeatPayload.headers.${index}.key`}
                                                        render={({ field }) => (
                                                            <FormItem className="flex-1">
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
                                                    </Button>
                                                </div>
                                            ))}
                                        </div>

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
                                    </Fragment>
                                )}

                                {selectedKind === "CONTAINER" && (
                                    <Fragment>
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

                                        <div className="space-y-2">
                                            <FormLabel>
                                                Command (optional)
                                                <Button
                                                    type="button"
                                                    variant="outline"
                                                    size="sm"
                                                    className="ml-2"
                                                    onClick={() => {
                                                        const currentCmds = form.watch("containerPayload.cmd") || []
                                                        form.setValue("containerPayload.cmd", [
                                                            ...currentCmds,
                                                            ""
                                                        ])
                                                    }}
                                                >
                                                    <Plus className="mr-1 h-3 w-3" /> Add argument
                                                </Button>
                                            </FormLabel>
                                            <FormDescription>
                                                Optional command and arguments to run in the container
                                            </FormDescription>

                                            {cmdFields?.map((_, index) => (
                                                <div key={index} className="flex items-center gap-2 mt-2">
                                                    <FormField
                                                        control={form.control}
                                                        name={`containerPayload.cmd.${index}`}
                                                        render={({ field }) => (
                                                            <FormItem className="flex-1">
                                                                <FormControl>
                                                                    <Input
                                                                        placeholder={"sh -c 'echo hello'"}
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
                                                            const updatedCmds = [...(form.watch("containerPayload.cmd") || [])]
                                                            updatedCmds.splice(index, 1)
                                                            form.setValue("containerPayload.cmd", updatedCmds)
                                                        }}
                                                    >
                                                        <Trash2 className="h-4 w-4" />
                                                    </Button>
                                                </div>
                                            ))}
                                        </div>

                                        <div className="space-y-2">
                                            <FormLabel>
                                                Environment variables (optional)
                                                <Button
                                                    type="button"
                                                    variant="outline"
                                                    size="sm"
                                                    className="ml-2"
                                                    onClick={() => {
                                                        const currentEnvs = form.watch("containerPayload.env") || []
                                                        form.setValue("containerPayload.env", [
                                                            ...currentEnvs,
                                                            ""
                                                        ])
                                                    }}
                                                >
                                                    <Plus className="mr-1 h-3 w-3" /> Add variable
                                                </Button>
                                            </FormLabel>
                                            <FormDescription>
                                                Optional environment variables to set in the container
                                            </FormDescription>

                                            {form.watch("containerPayload.env")?.map((_, index) => (
                                                <div key={index} className="flex items-center gap-2 mt-2">
                                                    <FormField
                                                        control={form.control}
                                                        name={`containerPayload.env.${index}`}
                                                        render={({ field }) => (
                                                            <FormItem className="flex-1">
                                                                <FormControl>
                                                                    <Input
                                                                        placeholder={"MY_ENV=VALUE"}
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
                                                            const updatedEnvs = [...(form.watch("containerPayload.env") || [])]
                                                            updatedEnvs.splice(index, 1)
                                                            form.setValue("containerPayload.env", updatedEnvs)
                                                        }}
                                                    >
                                                        <Trash2 className="h-4 w-4" />
                                                    </Button>
                                                </div>
                                            ))}
                                        </div>

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
                                    </Fragment>
                                )}
                            </CardContent>
                        </Card>

                        <FormField
                            control={form.control}
                            name="interval"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>Interval (minutes)</FormLabel>
                                    <FormControl>
                                        <Input
                                            type="number"
                                            min={1}
                                            {...field}
                                            value={field.value === undefined ? "" : field.value}
                                            onChange={(e) => {
                                                field.onChange(e.target.value === "" ? "" : Number(e.target.value));
                                            }}
                                        />
                                    </FormControl>
                                    <FormDescription>
                                        How often to run this workflow.
                                    </FormDescription>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <FormField
                            control={form.control}
                            name="maxConsecutiveJobFailuresAllowed"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>Max consecutive failures allowed</FormLabel>
                                    <FormControl>
                                        <Input
                                            type="number"
                                            min={3}
                                            {...field}
                                            value={field.value === undefined ? "" : field.value}
                                            onChange={(e) => {
                                                field.onChange(e.target.value === "" ? "" : Number(e.target.value));
                                            }}
                                        />
                                    </FormControl>
                                    <FormDescription>
                                        Maximum number of consecutive failures before the workflow is auto-disabled (default: 3).
                                    </FormDescription>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <DialogFooter className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                            <Button
                                type="button"
                                variant="outline"
                                onClick={() => onOpenChange(false)}
                                disabled={isCreating}
                                className="cursor-pointer w-full"
                            >
                                Cancel
                            </Button>
                            <Button
                                type="submit"
                                disabled={isCreating}
                                className="cursor-pointer w-full"
                            >
                                {isCreating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                Create workflow
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    )
}