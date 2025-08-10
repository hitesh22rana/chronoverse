"use client"

import { useState } from "react"
import { PlusCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Workflows } from "@/components/dashboard/workflows"
import { UserAnalyticsDrawer } from "@/components/dashboard/user-analytics-drawer"
import { CreateWorkflowDialog } from "@/components/dashboard/create-workflow-dialog"

export default function DashboardPage() {
    const [showCreateDialog, setShowCreateDialog] = useState(false)

    return (
        <div className="flex flex-col h-full">
            <div className="flex flex-col space-y-2 md:flex-row md:items-center md:justify-between md:space-y-0">
                <div>
                    <h2 className="text-xl font-bold tracking-tight">Dashboard</h2>
                    <p className="md:text-base text-sm text-muted-foreground">
                        Monitor and manage your automated workflows
                    </p>
                </div>
                <div className="flex flex-col md:flex-row items-center gap-2">
                    <UserAnalyticsDrawer />
                    <Button
                        className="w-full md:w-auto cursor-pointer"
                        onClick={() => setShowCreateDialog(true)}
                    >
                        <PlusCircle className="mr-2 h-4 w-4" />
                        Create workflow
                    </Button>
                </div>
            </div>

            <Workflows />

            <CreateWorkflowDialog
                open={showCreateDialog}
                onOpenChange={setShowCreateDialog}
            />
        </div>
    )
}