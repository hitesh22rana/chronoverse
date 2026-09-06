"use client"

import { WorkflowActionDialog, type WorkflowActionDialogProps } from "./workflow-action-dialog"
import { useWorkflowDetails } from "./use-workflow-details"

export function DeleteWorkflowDialog(props: WorkflowActionDialogProps) {
    const { deleteWorkflow, isDeleting } = useWorkflowDetails(props.workflow.id)
    return (
        <WorkflowActionDialog
            {...props}
            onConfirm={deleteWorkflow}
            isPending={isDeleting}
            title="Delete workflow"
            pendingLabel="Deleting..."
            description="This action cannot be undone, and will delete the workflow"
            warning="Deleting this workflow will remove it permanently from the system, including all its jobs and history. This action cannot be undone."
        />
    )
}
