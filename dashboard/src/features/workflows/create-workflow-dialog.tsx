"use client"

import { WorkflowNumberField, ContainerListField } from "./workflow-form-fields"

import { Fragment } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm, useWatch, type Resolver } from "react-hook-form"
import { createWorkflowSchema } from "./workflow-schemas"
import {
    Loader2,
    Plus,
    Trash2,
    Database
} from "lucide-react"

import { Switch } from "@/components/ui/switch"

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
    CardHeader,
    CardTitle
} from "@/components/ui/card"

import { useWorkflows } from "@/features/workflows/use-workflows"

type HeaderFormValue = {
    id?: string
    key: string
    value: string
}

type WorkflowFormValues = {
    name: string
    kind: "HEARTBEAT" | "CONTAINER"
    interval: number | string
    maxConsecutiveJobFailuresAllowed: number
    retainLogs: boolean
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

interface CreateWorkflowDialogProps {
    open: boolean
    onOpenChange: (_open: boolean) => void
}

const kindType = {
    'HEARTBEAT': "This workflow is used to monitor the availability of your services. It makes HTTP requests to specified endpoints at defined intervals.",
    'CONTAINER': "This workflow is used to run custom code in a containerized environment. It allows you to execute scripts or commands inside a specified container image."
}

type KindType = keyof typeof kindType

export function CreateWorkflowDialog({ open, onOpenChange }: CreateWorkflowDialogProps) {
    const { createWorkflow, isCreating } = useWorkflows()
    const form = useForm<WorkflowFormValues>({
        resolver: zodResolver(createWorkflowSchema) as Resolver<WorkflowFormValues>,
        defaultValues: {
            name: "",
            kind: "HEARTBEAT",
            interval: 5,
            maxConsecutiveJobFailuresAllowed: 3,
            retainLogs: true,
            heartbeatPayload: {
                endpoint: "",
                expectedStatusCode: 200,
                headers: [],
                timeout: "",
            }
        },
        mode: "onChange",
    })

    const watchedKind = useWatch({ control: form.control, name: "kind" })
    const selectedKind = watchedKind || "HEARTBEAT"

    const handleKindChange = (nextKind: KindType) => {
        form.setValue("kind", nextKind, { shouldDirty: true, shouldValidate: true })
        if (nextKind === "HEARTBEAT") {
            form.setValue("heartbeatPayload", {
                endpoint: "",
                expectedStatusCode: 200,
                headers: [],
                timeout: ""
            })
            form.unregister("containerPayload")
        } else {
            form.setValue("containerPayload", {
                image: "",
                cmd: [],
                cmdIds: [],
                env: [],
                envIds: [],
                timeout: ""
            })
            form.unregister("heartbeatPayload")
        }
    }

    const handleSubmit = (data: WorkflowFormValues) => {
        let payload: string = "{}"

        if (data.kind === "HEARTBEAT") {
            const { endpoint, expectedStatusCode, headers = [], timeout } = data.heartbeatPayload ?? {
                endpoint: "",
                expectedStatusCode: 200,
                headers: [],
                timeout: "",
            }
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
            const { image, cmd = [], env = [], timeout } = data.containerPayload ?? {
                image: "",
                cmd: [],
                env: [],
                timeout: "",
            }
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
            })
        }

        createWorkflow({
            name: data.name,
            kind: data.kind,
            payload: payload,
            interval: data.interval as number,
            max_consecutive_job_failures_allowed: data.maxConsecutiveJobFailuresAllowed,
            log_retention: data.retainLogs
        })
        form.reset()
        onOpenChange(false)
    }

    const watchedHeaders = useWatch({ control: form.control, name: "heartbeatPayload.headers" })
    const watchedCmd = useWatch({ control: form.control, name: "containerPayload.cmd" })
    const watchedCmdIds = useWatch({ control: form.control, name: "containerPayload.cmdIds" })
    const watchedEnv = useWatch({ control: form.control, name: "containerPayload.env" })
    const watchedEnvIds = useWatch({ control: form.control, name: "containerPayload.envIds" })
    const headerFields = selectedKind === "HEARTBEAT" ? watchedHeaders || [] : []
    const cmdFields = selectedKind === "CONTAINER" ? watchedCmd || [] : []
    const cmdFieldIds = selectedKind === "CONTAINER" ? watchedCmdIds || [] : []
    const envFields = selectedKind === "CONTAINER" ? watchedEnv || [] : []
    const envFieldIds = selectedKind === "CONTAINER" ? watchedEnvIds || [] : []

    return renderCreateWorkflowDialogView({
        open,
        isCreating,
        onOpenChange,
        form,
        handleSubmit,
        selectedKind,
        handleKindChange,
        headerFields,
        cmdFields,
        cmdFieldIds,
        envFields,
        envFieldIds,
    })
}

function renderCreateWorkflowDialogView(model: any) {
    const {
        open,
        isCreating,
        onOpenChange,
        form,
        handleSubmit,
        selectedKind,
        handleKindChange,
        headerFields,
        cmdFields,
        cmdFieldIds,
        envFields,
        envFieldIds,
    } = model

    return (
        <Dialog
            open={open}
            onOpenChange={(newOpen) => {
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
                    <form onSubmit={form.handleSubmit(handleSubmit)} className="space-y-6 pt-2">
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
                                        onValueChange={(value) => handleKindChange(value as KindType)}
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
                                        {kindType[selectedKind as KindType]}
                                    </FormDescription>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <Card>
                            <CardHeader>
                                <CardTitle>Configuration</CardTitle>
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
                                    </Fragment>
                                )}
                            </CardContent>
                        </Card>

                        <WorkflowNumberField name="interval" />

                        <WorkflowNumberField name="maxConsecutiveJobFailuresAllowed" />

                        {selectedKind === "CONTAINER" && (
                            <FormField
                                control={form.control}
                                name="retainLogs"
                                render={({ field }) => (
                                    <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                                        <div className="space-y-0.5">
                                            <div className="flex items-center gap-2">
                                                <Database className="h-4 w-4" />
                                                <FormLabel>Retain logs</FormLabel>
                                            </div>
                                            <FormDescription>
                                                Keep job logs for historical records and debugging purposes
                                            </FormDescription>
                                        </div>
                                        <FormControl>
                                            <Switch
                                                checked={field.value}
                                                onCheckedChange={field.onChange}
                                            />
                                        </FormControl>
                                    </FormItem>
                                )}
                            />
                        )}

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
