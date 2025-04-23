"use client"

import { Suspense, useState } from "react"

import { Header } from "@/components/dashboard/header"
import { NotificationsDrawer } from "@/components/dashboard/notifications-drawer"

import { useWorkflows } from "@/hooks/use-workflows"

import { cn } from "@/lib/utils"
import { Loader } from "lucide-react"

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {

  return (
    <Suspense fallback={
      <div className="flex items-center justify-center w-full h-full">
        <Loader className="w-8 h-8 animate-spin text-muted-foreground" />
      </div>
    }>
      <Layout>
        {children}
      </Layout>
    </Suspense>
  )
}

function Layout({
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