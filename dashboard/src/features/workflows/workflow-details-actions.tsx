"use client"

import { Edit, Trash2, XCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

import type { Workflow } from "@/features/workflows/types"

type WorkflowDetailsActionsProps = {
    workflow?: Workflow
    isLoading: boolean
    onEdit: () => void
    onTerminate: () => void
    onDelete: () => void
}

/**
 * Details tab action strip: editing is always offered, and exactly one
 * lifecycle action is offered alongside it — delete once the workflow is
 * terminated, terminate while it still runs. The destructive slot shows a
 * placeholder until the workflow is known, because which action it holds
 * depends on the loaded lifecycle state.
 */
export function WorkflowDetailsActions({
    workflow,
    isLoading,
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
            {isLoading ? (
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