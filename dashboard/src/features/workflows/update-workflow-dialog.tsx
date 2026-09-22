"use client"

import { WorkflowNumberField } from "./workflow-form-fields"

import { useEffect, useRef, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm, type Resolver } from "react-hook-form"
import { updateWorkflowSchema } from "./workflow-schemas"
import {
    Clock3,
    Settings2,
    Loader2,
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
    FormField,
    FormItem,
    FormLabel,
    FormMessage
} from "@/components/ui/form"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { Separator } from "@/components/ui/separator"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { WorkflowConfigurationFields } from "./workflow-configuration-fields"
import { serializeWorkflowPayload, type WorkflowConfigurationValues } from "./workflow-form-values"

import { useWorkflowDetails } from "@/features/workflows/use-workflow-details"

type UpdateWorkflowFormValues = WorkflowConfigurationValues & {
    name: string
    interval: number | string
    maxConsecutiveJobFailuresAllowed: number

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

export function UpdateWorkflowDialog(props: UpdateWorkflowDialogProps) {
    return props.open ? <UpdateWorkflowForm key={props.workflowId} {...props} /> : null
}

function UpdateWorkflowForm({
    workflowId,
    open,
    onOpenChange
}: UpdateWorkflowDialogProps) {
    const [section, setSection] = useState("configuration")
    const initKey = useRef<string | null>(null)
    const [ready, setReady] = useState(false)
    const {
        workflow,
        isLoading,
        isFetching,
        error,
        refetch,
        updateWorkflow,
        isUpdating
    } = useWorkflowDetails(workflowId);
    // The detail query serves stale cache first and refetches in the background.
    // Hold the form until that opening fetch settles successfully: on failure the
    // cached data is retained, so unlocking then would allow editing stale values.
    // The latch ignores later background polls.
    if (!isFetching && !error && !ready) setReady(true)

    const form = useForm<UpdateWorkflowFormValues>({
        resolver: zodResolver(updateWorkflowSchema) as Resolver<UpdateWorkflowFormValues>,
        defaultValues: {
            name: "",
            interval: 5,
            maxConsecutiveJobFailuresAllowed: 3,
        },
        mode: "onBlur",
        shouldFocusError: false,
    });

    useEffect(() => {
        if (!workflow) return;
        // The detail query serves stale cache first (30s staleTime) and refetches
        // in the background. Re-init while pristine so saving can't silently
        // overwrite a workflow that changed elsewhere; never clobber user edits.
        if (initKey.current === workflow.updated_at) return;
        if (initKey.current !== null && form.formState.isDirty) return;
        initKey.current = workflow.updated_at;

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
    }, [workflow, form, form.formState.isDirty]);

    const handleSubmit = (data: UpdateWorkflowFormValues) => {
        if (!workflow) return;


        updateWorkflow({
            name: data.name,
            payload: serializeWorkflowPayload(workflow.kind, data),
            interval: Number(data.interval),
            max_consecutive_job_failures_allowed: data.maxConsecutiveJobFailuresAllowed
        }, () => onOpenChange(false))
    };

    return (
        <Dialog open={open} onOpenChange={(nextOpen) => { if (!isUpdating) onOpenChange(nextOpen) }}>
            <DialogContent className="flex max-h-[90dvh] flex-col overflow-clip p-0 sm:max-w-2xl">
                <DialogHeader className="shrink-0 px-6 pt-6 pr-12">
                    <DialogTitle>Update workflow</DialogTitle>
                    <DialogDescription>
                        Edit your configuration or schedule, then save your changes.
                    </DialogDescription>
                </DialogHeader>

                {isLoading || !ready ? (
                    error && !isFetching ? (
                        <div className="flex flex-col items-center gap-3 my-8 px-6 text-center">
                            <p className="text-sm text-muted-foreground">Couldn&apos;t load the latest workflow data. Editing cached values could overwrite newer changes.</p>
                            <Button type="button" variant="outline" onClick={() => { void refetch() }}>
                                Retry
                            </Button>
                        </div>
                    ) : (
                        <div className="flex justify-center my-8">
                            <Loader2 className="h-8 w-8 animate-spin" />
                        </div>
                    )
                ) : workflow && (
                    <Form {...form}>
                        <form noValidate onSubmit={form.handleSubmit(handleSubmit, (errors) => {
                            setSection(errors.name || errors.heartbeatPayload || errors.containerPayload ? "configuration" : "schedule")
                            requestAnimationFrame(() => { void form.trigger(undefined, { shouldFocus: true }) })
                        })} className="flex min-h-0 flex-col overflow-clip">
                            <Tabs value={section} onValueChange={setSection} className="min-h-0 gap-0">
                                <div className="shrink-0 px-6 pb-4">
                                    <TabsList aria-label="Workflow settings" className="w-full">
                                        <TabsTrigger value="configuration" disabled={isUpdating}><Settings2 /> Configuration</TabsTrigger>
                                        <TabsTrigger value="schedule" disabled={isUpdating}><Clock3 /> Schedule</TabsTrigger>
                                    </TabsList>
                                </div>
                                <div className="min-h-0 overflow-y-auto px-6 pb-6">
                                    <fieldset disabled={isUpdating} className="min-w-0">
                                    <legend className="sr-only">Workflow settings</legend>
                                    <TabsContent value="configuration" forceMount className="data-[state=inactive]:hidden">
                                        <div className="flex flex-col gap-6">
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

                                            <WorkflowConfigurationFields kind={workflow.kind} />

                                        </div>
                                    </TabsContent>
                                    <TabsContent value="schedule" forceMount className="data-[state=inactive]:hidden">
                                        <div className="flex flex-col gap-6">
                                    <div className="flex flex-col gap-1">
                                        <h3 className="font-semibold">Schedule & failure handling</h3>
                                        <p className="text-sm text-muted-foreground">Choose how often this workflow runs and when to pause after failures.</p>
                                    </div>
                                            <WorkflowNumberField name="interval" />

                                            <WorkflowNumberField name="maxConsecutiveJobFailuresAllowed" />

                                        </div>
                                    </TabsContent>
                                    </fieldset>
                                </div>
                            </Tabs>
                            <Separator />
                            <DialogFooter className="grid shrink-0 grid-cols-2 items-center gap-2 px-4 py-4 sm:gap-4 sm:px-6">
                                <Button
                                    type="button"
                                    variant="outline"
                                    onClick={() => onOpenChange(false)}
                                    disabled={isUpdating}
                                    className="w-full"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    disabled={isUpdating}
                                    className="w-full"
                                >
                                    {isUpdating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                    {isUpdating ? "Saving…" : "Save changes"}
                                </Button>
                            </DialogFooter>
                        </form>
                    </Form>
                )}
            </DialogContent>
        </Dialog>
    );
}
