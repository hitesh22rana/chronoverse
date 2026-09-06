"use client"

import { WorkflowActionDialog, type WorkflowActionDialogProps } from "./workflow-action-dialog"
import { useWorkflowDetails } from "./use-workflow-details"

export function TerminateWorkflowDialog(props: WorkflowActionDialogProps) {
    const { terminateWorkflow, isTerminating } = useWorkflowDetails(props.workflow.id)
    return (
        <WorkflowActionDialog
            {...props}
            onConfirm={terminateWorkflow}
            isPending={isTerminating}
            title="Terminate workflow"
            pendingLabel="Terminating..."
            description="This action will cancel all remaining jobs and scheduled executions for this workflow."
            warning="Terminating this workflow will terminate all ongoing jobs and prevent any future scheduled executions."
        />
    )
}
