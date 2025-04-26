"use client"

import { Fragment } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import {
    AlertTriangle,
    Loader2
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
    FormMessage
} from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"

import { useWorkflowDetails } from "@/hooks/use-workflow-details"
import { Workflow } from "@/hooks/use-workflows"

interface TerminateWorkflowDialogProps {
    workflow: Workflow
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function TerminateWorkflowDialog({
    workflow,
    open,
    onOpenChange
}: TerminateWorkflowDialogProps) {
    const { terminateWorkflow, isTerminating } = useWorkflowDetails(workflow.id)

    const FormSchema = z.object({
        confirmName: z.string().refine(value => value === workflow.name, {
            message: "Workflow name doesn't match"
        })
    })

    const form = useForm<z.infer<typeof FormSchema>>({
        resolver: zodResolver(FormSchema),
        defaultValues: {
            confirmName: ""
        },
        mode: "onSubmit",
    })

    const handleTerminate = () => {
        terminateWorkflow()
        onOpenChange(false)
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-lg">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <AlertTriangle className="h-5 w-5 text-destructive" />
                        Terminate workflow
                    </DialogTitle>
                    <DialogDescription>
                        This action cannot be undone, and will cancel all remaining
                        jobs and scheduled executions for this workflow.
                    </DialogDescription>
                </DialogHeader>

                <div className="py-2">
                    <div className="rounded-md bg-destructive/10 p-4 mb-4">
                        <p className="flex-grow sm:text-sm text-xs text-destructive">
                            Deleting this workflow will terminate all ongoing jobs
                            and prevent any future scheduled executions.
                        </p>
                    </div>

                    <Form {...form}>
                        <form onSubmit={form.handleSubmit(handleTerminate)} className="space-y-4">
                            <FormField
                                control={form.control}
                                name="confirmName"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormControl>
                                            <div className="space-y-2">
                                                <p className="text-sm text-muted-foreground">
                                                    To confirm, type <span className="font-medium text-foreground">{workflow.name}</span>
                                                </p>
                                                <Input
                                                    placeholder="Enter workflow name"
                                                    {...field}
                                                    autoComplete="off"
                                                    disabled={isTerminating}
                                                />
                                            </div>
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <DialogFooter className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                                <Button
                                    type="button"
                                    variant="outline"
                                    onClick={() => onOpenChange(false)}
                                    disabled={isTerminating}
                                    className="cursor-pointer w-full"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    variant="destructive"
                                    disabled={isTerminating || !form.formState.isValid}
                                    className="cursor-pointer w-full"
                                >
                                    {isTerminating ? (
                                        <Fragment>
                                            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                            Deleting...
                                        </Fragment>
                                    ) : (
                                        "Terminate workflow"
                                    )}
                                </Button>
                            </DialogFooter>
                        </form>
                    </Form>
                </div>
            </DialogContent>
        </Dialog>
    )
}