"use client"

import { WorkflowNumberField, ContainerListField } from "./workflow-form-fields"

import { Fragment, useEffect } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm, useWatch, type Resolver } from "react-hook-form"
import { updateWorkflowSchema } from "./workflow-schemas"
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
    CardHeader,
    CardTitle
} from "@/components/ui/card"

import { useWorkflowDetails } from "@/features/workflows/use-workflow-details"

type HeaderFormValue = {
    id?: string
    key: string
    value: string
}

type UpdateWorkflowFormValues = {
    name: string
    interval: number | string
    maxConsecutiveJobFailuresAllowed: number
    heartbeatPayload?: {
        endpoint: string
        expectedStatusCode: number
        headers: HeaderFormValue[]
        timeout: string
    }
    containerPayload?: {
        image: string
        cmd: string[]
        cmdIds?: string[]
        env: string[]
        envIds?: string[]
        timeout: string
    }
}

type WorkflowPayload = {
    endpoint?: string
    expected_status_code?: number
    headers?: Record<string, string>
    image?: string
    cmd?: string[]
    env?: Record<string, string>
    timeout?: string
}

interface UpdateWorkflowDialogProps {
    workflowId: string;
    open: boolean;
    onOpenChange: (_open: boolean) => void;
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

    const form = useForm<UpdateWorkflowFormValues>({
        resolver: zodResolver(updateWorkflowSchema) as Resolver<UpdateWorkflowFormValues>,
        defaultValues: {
            name: "",
            interval: 5,
            maxConsecutiveJobFailuresAllowed: 3,
        },
        mode: "onBlur",
    });

    useEffect(() => {
        if (!workflow) return;

        const parsedPayload = workflow.payload ? JSON.parse(workflow.payload) as WorkflowPayload : {};

        if (workflow.kind === "HEARTBEAT") {
            const headers = parsedPayload.headers ?
                Object.entries(parsedPayload.headers).map(([key, value]) => ({ id: crypto.randomUUID(), key, value })) :
                [];

            form.setValue("heartbeatPayload", {
                endpoint: parsedPayload.endpoint || "",
                expectedStatusCode: parsedPayload.expected_status_code || 200,
                headers,
                timeout: parsedPayload.timeout || ""
            });
        }
        else if (workflow.kind === "CONTAINER") {
            let envArray: string[] = [];
            if (parsedPayload.env && typeof parsedPayload.env === 'object') {
                envArray = Object.entries(parsedPayload.env).map(
                    ([key, value]) => `${key}=${value}`
                );
            }

            form.setValue("containerPayload", {
                image: parsedPayload.image || "",
                cmd: parsedPayload.cmd || [],
                cmdIds: (parsedPayload.cmd || []).map(() => crypto.randomUUID()),
                env: envArray,
                envIds: envArray.map(() => crypto.randomUUID()),
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

    const handleSubmit = (data: UpdateWorkflowFormValues) => {
        if (!workflow) return;

        let payload = "{}";

        if (workflow.kind === "HEARTBEAT") {
            const { endpoint, expectedStatusCode, headers = [], timeout } = data.heartbeatPayload ?? {
                endpoint: "",
                expectedStatusCode: 200,
                headers: [],
                timeout: "",
            };
            const headersObject = headers.reduce((acc, header) => {
                if (header.key) {
                    acc[header.key] = header.value;
                }
                return acc;
            }, {} as Record<string, string>);

            payload = JSON.stringify({
                endpoint,
                expected_status_code: expectedStatusCode || 200,
                headers: headersObject,
                ...(timeout ? { timeout } : {})
            });
        } else if (workflow.kind === "CONTAINER") {
            const { image, cmd = [], env = [], timeout } = data.containerPayload ?? {
                image: "",
                cmd: [],
                env: [],
                timeout: "",
            };
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
                ...(timeout ? { timeout } : {})
            });
        }

        updateWorkflow({
            name: data.name,
            payload: payload,
            interval: Number(data.interval),
            max_consecutive_job_failures_allowed: data.maxConsecutiveJobFailuresAllowed
        })
        form.reset();
        onOpenChange(false);
    };

    const watchedHeaders = useWatch({ control: form.control, name: "heartbeatPayload.headers" })
    const watchedCmd = useWatch({ control: form.control, name: "containerPayload.cmd" })
    const watchedCmdIds = useWatch({ control: form.control, name: "containerPayload.cmdIds" })
    const watchedEnv = useWatch({ control: form.control, name: "containerPayload.env" })
    const watchedEnvIds = useWatch({ control: form.control, name: "containerPayload.envIds" })
    const headerFields = workflow?.kind === "HEARTBEAT" ? watchedHeaders || [] : []
    const cmdFields = workflow?.kind === "CONTAINER" ? watchedCmd || [] : []
    const cmdFieldIds = workflow?.kind === "CONTAINER" ? watchedCmdIds || [] : []
    const envFields = workflow?.kind === "CONTAINER" ? watchedEnv || [] : []
    const envFieldIds = workflow?.kind === "CONTAINER" ? watchedEnvIds || [] : []

    return renderUpdateWorkflowDialogView({
        open,
        onOpenChange,
        isLoading,
        workflow,
        form,
        handleSubmit,
        headerFields,
        cmdFields,
        cmdFieldIds,
        envFields,
        envFieldIds,
        isUpdating,
    })
}

function renderUpdateWorkflowDialogView(model: any) {
    const {
        open,
        onOpenChange,
        isLoading,
        workflow,
        form,
        handleSubmit,
        headerFields,
        cmdFields,
        cmdFieldIds,
        envFields,
        envFieldIds,
        isUpdating,
    } = model

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
                                    <CardTitle>Configuration</CardTitle>
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

                                            <WorkflowNumberField name="heartbeatPayload.expectedStatusCode" />

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
                                                                    <FormLabel className="sr-only">Header {index + 1} value</FormLabel>
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
                                                            <span className="sr-only">Remove header {index + 1}</span>
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
                                                                placeholder="30s"
                                                                {...field}
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

                            <WorkflowNumberField name="interval" />

                            <WorkflowNumberField name="maxConsecutiveJobFailuresAllowed" />

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
