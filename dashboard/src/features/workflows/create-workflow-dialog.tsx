"use client"

import { WorkflowNumberField } from "./workflow-form-fields"

import { useEffect, useRef, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { useForm, useWatch, type Resolver } from "react-hook-form"
import { createWorkflowStepSchemas } from "./workflow-schemas"
import {
    ArrowLeft,
    ArrowRight,
    Check,
    Loader2,
    Database
} from "lucide-react"

import { cn } from "@/lib/utils"
import { Separator } from "@/components/ui/separator"

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
    SelectGroup,
    SelectTrigger,
    SelectValue
} from "@/components/ui/select"
import { WorkflowConfigurationFields } from "./workflow-configuration-fields"
import { serializeWorkflowPayload, type WorkflowConfigurationValues } from "./workflow-form-values"

import { useWorkflows } from "@/features/workflows/use-workflows"

type WorkflowFormValues = WorkflowConfigurationValues & {
    name: string
    kind: "HEARTBEAT" | "CONTAINER"
    interval: number | string
    maxConsecutiveJobFailuresAllowed: number
    retainLogs: boolean

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

const steps = [
    { title: "Basics", description: "Name & workflow kind" },
    { title: "Configuration", description: "Set up what runs" },
    { title: "Schedule", description: "Timing & retention" },
]

export function CreateWorkflowDialog(props: CreateWorkflowDialogProps) {
    return props.open ? <CreateWorkflowForm {...props} /> : null
}

function CreateWorkflowForm({ open, onOpenChange }: CreateWorkflowDialogProps) {
    const [step, setStep] = useState(0)
    const stepHeading = useRef<HTMLHeadingElement>(null)
    const previousStep = useRef(step)
    useEffect(() => {
        if (previousStep.current !== step) stepHeading.current?.focus()
        previousStep.current = step
    }, [step])
    const { createWorkflow, isCreating } = useWorkflows()
    const form = useForm<WorkflowFormValues>({
        resolver: zodResolver(createWorkflowStepSchemas[step]) as Resolver<WorkflowFormValues>,
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
            },
            containerPayload: { image: "", cmd: [], cmdIds: [], env: [], envIds: [], timeout: "" },
        },
        mode: "onChange",
    })

    const watchedKind = useWatch({ control: form.control, name: "kind" })
    const selectedKind = watchedKind || "HEARTBEAT"

    const handleKindChange = (nextKind: KindType) => {
        form.setValue("kind", nextKind, { shouldDirty: true, shouldValidate: true })
        form.clearErrors(["heartbeatPayload", "containerPayload"])
    }

    const handleSubmit = (data: WorkflowFormValues) => {
        if (isCreating) return
        if (step < 2) {
            setStep(step + 1)
            return
        }

        createWorkflow({
            name: data.name,
            kind: data.kind,
            payload: serializeWorkflowPayload(data.kind, data),
            interval: data.interval as number,
            max_consecutive_job_failures_allowed: data.maxConsecutiveJobFailuresAllowed,
            log_retention: data.retainLogs
        }, () => onOpenChange(false))
    }

    return (
        <Dialog
            open={open}
            onOpenChange={(newOpen) => {
                if (isCreating && !newOpen) return;
                onOpenChange(newOpen)
            }}
        >
            <DialogContent className="flex max-h-[90dvh] flex-col overflow-clip p-0 sm:max-w-2xl">
                <DialogHeader className="shrink-0 px-6 pt-6 pr-12">
                    <DialogTitle>Create new workflow</DialogTitle>
                    <DialogDescription>
                        Choose what runs, configure it, then set the schedule.
                    </DialogDescription>
                </DialogHeader>

                <WorkflowProgress step={step} />

                <Form {...form}>
                    <form noValidate onSubmit={form.handleSubmit(handleSubmit)} className="flex min-h-0 flex-col overflow-clip">
                        <div className="min-h-0 overflow-y-auto px-6 pb-6">
                            <fieldset disabled={isCreating} className="min-w-0">
                                <legend className="sr-only">Workflow settings</legend>
                                <div className="mb-5 mt-1">
                                    <p className="text-xs text-muted-foreground" aria-live="polite">Step {step + 1} of {steps.length}</p>
                                    <h3 ref={stepHeading} tabIndex={-1} className="mt-1 font-semibold outline-none">{steps[step].title}</h3>
                                    <p className="text-sm text-muted-foreground">
                                        {step === 0 ? "Give your workflow a name and choose how it runs."
                                            : step === 1 ? (selectedKind === "HEARTBEAT" ? "Configure the HTTP request to monitor your service." : "Choose a container image and customize its execution.")
                                            : "Choose how often to run and what happens after failures."}
                                    </p>
                                </div>
                                <div hidden={step !== 0}>
                                    <div className="flex flex-col gap-6">
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
                                                            <SelectGroup>
                                                                <SelectItem value="HEARTBEAT">
                                                                    <span>Heartbeat</span>
                                                                </SelectItem>
                                                                <SelectItem value="CONTAINER">
                                                                    <span>Container</span>
                                                                </SelectItem>
                                                            </SelectGroup>
                                                        </SelectContent>
                                                    </Select>
                                                    <FormDescription>
                                                        {kindType[selectedKind as KindType]}
                                                    </FormDescription>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                    </div>
                                </div>
                                <div hidden={step !== 1}>
                                    <WorkflowConfigurationFields kind={selectedKind} />

                                </div>
                                <div hidden={step !== 2}>
                                    <div className="flex flex-col gap-6">
                                        <WorkflowNumberField name="interval" />

                                        <WorkflowNumberField name="maxConsecutiveJobFailuresAllowed" />

                                        {selectedKind === "CONTAINER" && (
                                            <FormField
                                                control={form.control}
                                                name="retainLogs"
                                                render={({ field }) => (
                                                    <FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
                                                        <div className="flex flex-col gap-0.5">
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

                                    </div>
                                </div>
                            </fieldset>
                        </div>
                        <Separator />
                        <DialogFooter className="grid shrink-0 grid-cols-2 items-center gap-2 px-4 py-4 sm:flex sm:justify-between sm:px-6">
                            {step === 0 ? (
                                <Button type="button" variant="outline" className="h-10 py-0" onClick={() => onOpenChange(false)} disabled={isCreating}>
                                    Cancel
                                </Button>
                            ) : (
                                <Button type="button" variant="outline" className="h-10 py-0" onClick={() => setStep(step - 1)} disabled={isCreating}>
                                    <ArrowLeft data-icon="inline-start" /> Previous
                                </Button>
                            )}
                            <Button type="submit" className="h-10 min-w-0 shrink whitespace-normal px-2 py-0 sm:px-4" disabled={isCreating || form.formState.isValidating}>
                                {isCreating && <Loader2 data-icon="inline-start" className="animate-spin" />}
                                {step === 2 ? (isCreating ? "Creating…" : "Create workflow") : "Next"}
                                {step < 2 && <ArrowRight data-icon="inline-end" />}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    )
}

function WorkflowProgress({ step }: { step: number }) {
    return (
        <ol aria-label="Workflow setup progress" className="grid shrink-0 grid-cols-3 px-4 py-2 sm:px-6">
            {steps.map((item, index) => (
                <li key={item.title} aria-current={index === step ? "step" : undefined}
                    className="relative flex min-w-0 flex-col items-center gap-3 text-center">
                    <div className="flex justify-center">
                        <span className={cn("flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-medium",
                            index <= step ? "border-primary bg-primary text-primary-foreground" : "text-muted-foreground")}>
                            {index < step ? <Check className="size-3.5" aria-hidden="true" /> : index + 1}
                        </span>
                        {index < steps.length - 1 && <Separator className={cn("absolute top-3.5 left-[calc(50%+1.375rem)] data-[orientation=horizontal]:w-[calc(100%-2.75rem)]", index < step && "bg-primary")} />}
                    </div>
                    <div>
                        <p className={cn("text-[11px] font-medium sm:text-sm", index > step && "text-muted-foreground")}>{item.title}</p>
                        <p className="hidden text-xs text-muted-foreground sm:block">{item.description}</p>
                        {index < step && <span className="sr-only">Completed</span>}
                    </div>
                </li>
            ))}
        </ol>
    )
}
