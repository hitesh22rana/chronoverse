"use client"

import { Edit, Trash2, XCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

import type { Workflow } from "@/features/workflows/types"

type WorkflowDetailsActionsProps = {
    workflow?: Workflow
    onEdit: () => void
    onTerminate: () => void
    onDelete: () => void
}

/**
 * Editing is always offered; the second slot holds exactly one lifecycle action
 * — delete once terminated, terminate until then. A missing workflow leaves the
 * lifecycle unknown, whether the detail query is still loading or has failed, so
 * that slot becomes a placeholder instead of offering an action the page cannot
 * act on: the lifecycle dialogs only mount once a workflow has loaded cleanly.
 */
export function WorkflowDetailsActions({
    workflow,
    onEdit,
    onTerminate,
    onDelete,
}: WorkflowDetailsActionsProps) {
    return (
        <div className="flex sm:flex-row flex-col items-center justify-end mb-4 gap-2 w-full">
            <Button
                variant="outline"
                size="sm"
                className="cursor-pointer shrink-0 sm:max-w-[140px] w-full h-9"
                onClick={onEdit}
            >
                <Edit className="h-4 w-4" />
                Edit workflow
            </Button>
            {workflow === undefined ? (
                <Skeleton className="h-9 sm:max-w-[180px] w-full rounded-md" />
            ) : workflow?.terminated_at ? (
                <Button
                    variant="destructive"
                    size="sm"
                    className="cursor-pointer shrink-0 sm:max-w-[180px] w-full h-9"
                    onClick={onDelete}
                >
                    <Trash2 className="h-4 w-4" />
                    Delete workflow
                </Button>
            ) : (
                <Button
                    variant="secondary"
                    size="sm"
                    className="cursor-pointer shrink-0 sm:max-w-[180px] w-full h-9"
                    onClick={onTerminate}
                >
                    <XCircle className="h-4 w-4" />
                    Terminate workflow
                </Button>
            )}
        </div>
    )
}
