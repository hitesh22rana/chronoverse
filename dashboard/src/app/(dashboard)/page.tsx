"use client"

import { useState } from "react"
import { PlusCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Workflows } from "@/components/dashboard/workflows"
import { CreateWorkflowDialog } from "@/components/dashboard/create-workflow-dialog"

export default function DashboardPage() {
    const [showCreateDialog, setShowCreateDialog] = useState(false)

    return (
        <div className="space-y-6">
            <div className="flex flex-col space-y-2 md:flex-row md:items-center md:justify-between md:space-y-0">
                <div>
                    <h2 className="text-xl font-bold tracking-tight">Dashboard</h2>
                    <p className="text-muted-foreground">
                        Monitor and manage your automated workflows
                    </p>
                </div>
                <Button
                    className="w-full md:w-auto cursor-pointer"
                    onClick={() => setShowCreateDialog(true)}
                >
                    <PlusCircle className="mr-2 h-4 w-4" />
                    Create Workflow
                </Button>
            </div>

            <Workflows />

            <CreateWorkflowDialog
                open={showCreateDialog}
                onOpenChange={setShowCreateDialog}
            />
        </div>
    )
}