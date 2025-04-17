"use client"

import { useState } from "react"

import { Header } from "@/components/dashboard/header"
import { NotificationsDrawer } from "@/components/dashboard/notifications-drawer"

import { useWorkflows } from "@/hooks/use-workflows"

import { cn } from "@/lib/utils"

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const [notificationsOpen, setNotificationsOpen] = useState(false)

  const { workflows } = useWorkflows()

  return (
    <div className={cn(
      "flex flex-col w-full overflow-hidden",
      workflows.length == 0 && "h-svh"
    )}>
      <Header onNotificationsClick={() => setNotificationsOpen(true)} />
      <main className="flex-1 flex flex-col overflow-hidden bg-background/95 md:p-6 p-4">
        {children}
      </main>
      <NotificationsDrawer
        open={notificationsOpen}
        onClose={() => setNotificationsOpen(false)}
      />
    </div>
  )
}