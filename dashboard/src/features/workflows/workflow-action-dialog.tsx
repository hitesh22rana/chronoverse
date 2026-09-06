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
    FormLabel,
    FormMessage
} from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"

import type { Workflow } from "@/features/workflows/types"

export interface WorkflowActionDialogProps {
    workflow: Workflow
    open: boolean
    onOpenChange: (_open: boolean) => void
}

export function WorkflowActionDialog({ workflow, open, onOpenChange, onConfirm, isPending, title, description, warning, pendingLabel }: WorkflowActionDialogProps & {
    onConfirm: () => void
    isPending: boolean
    title: string
    description: string
    warning: string
    pendingLabel: string
}) {

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

    const handleConfirm = () => {
        onConfirm()
        onOpenChange(false)
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-lg">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <AlertTriangle className="h-5 w-5 text-destructive" />
                        {title}
                    </DialogTitle>
                    <DialogDescription>
                        {description}
                    </DialogDescription>
                </DialogHeader>

                <div className="py-2">
                    <div className="rounded-md bg-destructive/10 p-4 mb-4">
                        <p className="grow sm:text-sm text-xs text-destructive">
                            {warning}
                        </p>
                    </div>

                    <Form {...form}>
                        <form onSubmit={form.handleSubmit(handleConfirm)} className="space-y-4">
                            <FormField
                                control={form.control}
                                name="confirmName"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel className="text-sm text-muted-foreground">
                                            To confirm, type <span className="font-medium text-foreground">{workflow.name}</span>
                                        </FormLabel>
                                        <FormControl>
                                            <Input placeholder="Enter workflow name" {...field} autoComplete="off" disabled={isPending} />
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
                                    disabled={isPending}
                                    className="cursor-pointer w-full"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    variant="destructive"
                                    disabled={isPending || !form.formState.isValid}
                                    className="cursor-pointer w-full"
                                >
                                    {isPending ? (
                                        <Fragment>
                                            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                            {pendingLabel}
                                        </Fragment>
                                    ) : (
                                        title
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
