"use client"

import { useState } from "react"

import { Header } from "@/components/dashboard/header"
import { NotificationsDrawer } from "@/components/dashboard/notifications-drawer"

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const [notificationsOpen, setNotificationsOpen] = useState(false)

  return (
    <div className="flex flex-col h-svh w-full overflow-hidden">
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