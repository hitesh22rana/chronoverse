"use client"

import { useState } from "react"
import dynamic from "next/dynamic"
import { BarChart3, PlusCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Workflows } from "@/components/dashboard/workflows"
import { CreateWorkflowDialog } from "@/components/dashboard/create-workflow-dialog"

const UserAnalyticsDrawer = dynamic(
    () => import("@/features/analytics/user").then((module) => module.UserAnalyticsDrawer),
    {
        ssr: false,
        loading: () => (
            <Button
                variant="outline"
                className="w-full cursor-pointer md:w-fit"
                aria-hidden="true"
                tabIndex={-1}
            >
                <BarChart3 data-icon="inline-start" />
                Analytics
            </Button>
        ),
    },
)

export default function DashboardPage() {
    const [showCreateDialog, setShowCreateDialog] = useState(false)

    return (
        <div className="flex min-h-0 flex-1 flex-col">
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

            {showCreateDialog && (
                <CreateWorkflowDialog
                    open
                    onOpenChange={setShowCreateDialog}
                />
            )}
        </div>
    )
}
