"use client"

import { Fragment, useEffect, useMemo } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm } from "react-hook-form"
import { z } from "zod"
import { Duration, parseDuration } from "@alwatr/parse-duration"
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
    Card,
    CardContent,
    CardDescription,
    CardHeader,
    CardTitle
} from "@/components/ui/card"

import { useWorkflowDetails } from "@/hooks/use-workflow-details"

// Base schema for update workflow
const baseUpdateWorkflowSchema = z.object({
    name: z.string().min(3, "Name must be at least 3 characters").max(50, "Name must be at most 50 characters"),
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
    maxConsecutiveJobFailuresAllowed: z.coerce.number().int().min(0).max(100)
})

// Heartbeat payload schema
const heartbeatPayloadSchema = z.object({
    endpoint: z.string().url("Please enter a valid URL"),
    headers: z.array(
        z.object({
            key: z.string().min(1, "Header key is required"),
            value: z.string()
        })
    ).default([])
})

// Container payload schema
const containerPayloadSchema = z.object({
    image: z.string().min(1, "Container image is required"),
    cmd: z.array(z.string())
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
        }, "Timeout must be a valid duration (e.g., '30s', '5m') up to 1 hour")
})

interface UpdateWorkflowDialogProps {
    workflowId: string;
    open: boolean;
    onOpenChange: (open: boolean) => void;
}

const kindType = {
    'HEARTBEAT': {
        title: "Heartbeat workflow",
        description: "HEARTBEAT workflows are used to monitor the availability of external services.",
    },
    'CONTAINER': {
        title: "Container workflow",
        description: "Container workflows are used to run custom code in a containerized environment.",
    }
}

export function UpdateWorkflowDialog({
    workflowId,
    open,
    onOpenChange
}: UpdateWorkflowDialogProps) {
    const {
        workflow,
        isLoading,
        updateWorkflow,
        isUpdating
    } = useWorkflowDetails(workflowId);

    // Create dynamic schema based on workflow kind
    const updateWorkflowSchema = useMemo(() => {
        if (!workflow) return baseUpdateWorkflowSchema;

        switch (workflow.kind) {
            case "HEARTBEAT":
                return baseUpdateWorkflowSchema.extend({
                    heartbeatPayload: heartbeatPayloadSchema,
                });
            case "CONTAINER":
                return baseUpdateWorkflowSchema.extend({
                    containerPayload: containerPayloadSchema,
                });
            default:
                return baseUpdateWorkflowSchema;
        }
    }, [workflow]);

    const form = useForm({
        resolver: zodResolver(updateWorkflowSchema),
        defaultValues: {
            name: "",
            interval: 5,
            maxConsecutiveJobFailuresAllowed: 3,
        },
        mode: "onBlur",
    });

    // Initialize payload when workflow data is loaded
    useEffect(() => {
        if (!workflow) return;

        const parsedPayload = workflow.payload ? JSON.parse(workflow.payload) : {};

        if (workflow.kind === "HEARTBEAT") {
            // Prepare headers array from object
            const headers = parsedPayload.headers ?
                Object.entries(parsedPayload.headers).map(([key, value]) => ({ key, value })) :
                [];

            form.setValue("heartbeatPayload", {
                endpoint: parsedPayload.endpoint || "",
                headers
            });
        }
        else if (workflow.kind === "CONTAINER") {
            form.setValue("containerPayload", {
                image: parsedPayload.image || "",
                cmd: parsedPayload.cmd || [""],
                timeout: parsedPayload.timeout || ""
            });
        }

        form.reset({
            name: workflow.name,
            interval: workflow.interval,
            maxConsecutiveJobFailuresAllowed: workflow.max_consecutive_job_failures_allowed,
            ...(workflow.kind === "HEARTBEAT" ? {
                heartbeatPayload: form.getValues("heartbeatPayload")
            } : {}),
            ...(workflow.kind === "CONTAINER" ? {
                containerPayload: form.getValues("containerPayload")
            } : {})
        });
    }, [workflow, form]);

    const handleSubmit = (data) => {
        // Don't proceed if no workflow data
        if (!workflow) return;

        // Prepare the payload based on workflow kind
        let payload = "{}";

        if (workflow.kind === "HEARTBEAT") {
            const { endpoint, headers = [] } = data.heartbeatPayload;
            const headersObject = headers.reduce((acc, header) => {
                if (header.key) {
                    acc[header.key] = header.value;
                }
                return acc;
            }, {});

            payload = JSON.stringify({
                endpoint,
                headers: headersObject
            });
        } else if (workflow.kind === "CONTAINER") {
            const { image, cmd, timeout } = data.containerPayload;
            payload = JSON.stringify({
                image,
                ...(cmd && cmd.length > 0 ? { cmd } : {}),
                ...(timeout ? { timeout } : {})
            });
        }

        // Call update API with the constructed payload
        updateWorkflow({
            name: data.name,
            payload: payload,
            interval: data.interval,
            max_consecutive_job_failures_allowed: data.maxConsecutiveJobFailuresAllowed
        })
        form.reset();
        onOpenChange(false);
    };

    // Get current field values based on kind
    const headerFields = workflow?.kind === "HEARTBEAT"
        ? form.watch("heartbeatPayload.headers") || []
        : [];

    const cmdFields = workflow?.kind === "CONTAINER"
        ? form.watch("containerPayload.cmd") || []
        : [];

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-2xl max-h-[95vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>Update workflow</DialogTitle>
                    <DialogDescription>
                        Modify your workflow configuration.
                    </DialogDescription>
                </DialogHeader>

                {isLoading ? (
                    <div className="flex justify-center my-8">
                        <Loader2 className="h-8 w-8 animate-spin" />
                    </div>
                ) : workflow && (
                    <Form {...form}>
                        <form onSubmit={form.handleSubmit(handleSubmit)} className="space-y-6 pt-2">
                            <FormField
                                control={form.control}
                                name="name"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Name</FormLabel>
                                        <FormControl>
                                            <Input placeholder="Workflow Name" {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <Card>
                                <CardHeader>
                                    <CardTitle>{workflow.kind} Configuration</CardTitle>
                                    <CardDescription>
                                        {kindType[workflow.kind]?.description || "Configure your workflow settings."}
                                    </CardDescription>
                                </CardHeader>
                                <CardContent className="space-y-4">
                                    {workflow.kind === "HEARTBEAT" && (
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
                                                            />
                                                        </FormControl>
                                                        <FormDescription>
                                                            The URL to send the heartbeat request to
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
                                                                            placeholder="Header Name"
                                                                            {...field}
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
                                        </Fragment>
                                    )}

                                    {workflow.kind === "CONTAINER" && (
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
                                                            form.setValue("containerPayload.cmd", [
                                                                ...cmdFields,
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

                                                {cmdFields.map((_, index) => (
                                                    <div key={index} className="flex items-center gap-2 mt-2">
                                                        <FormField
                                                            control={form.control}
                                                            name={`containerPayload.cmd.${index}`}
                                                            render={({ field }) => (
                                                                <FormItem className="flex-1">
                                                                    <FormControl>
                                                                        <Input
                                                                            placeholder={""}
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
                                                                const updatedCmds = [...cmdFields]
                                                                updatedCmds.splice(index, 1)
                                                                form.setValue("containerPayload.cmd", updatedCmds)
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
                                                            />
                                                        </FormControl>
                                                        <FormDescription>
                                                            Maximum execution time (e.g., &quot;30s&quot;, &quot;5m&quot;), up to 1 hour
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
                                                min={0}
                                                {...field}
                                                value={field.value === undefined ? "" : field.value}
                                                onChange={(e) => {
                                                    field.onChange(e.target.value === "" ? "" : Number(e.target.value));
                                                }}
                                            />
                                        </FormControl>
                                        <FormDescription>
                                            Maximum number of consecutive failures before the workflow is disabled
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
                                    disabled={isUpdating}
                                    className="cursor-pointer w-full"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    disabled={isUpdating}
                                    className="cursor-pointer w-full"
                                >
                                    {isUpdating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                    Update workflow
                                </Button>
                            </DialogFooter>
                        </form>
                    </Form>
                )}
            </DialogContent>
        </Dialog>
    );
}