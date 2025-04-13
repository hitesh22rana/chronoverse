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
    <div className="flex h-screen">
      <div className="flex flex-col flex-1 overflow-hidden">
        <Header onNotificationsClick={() => setNotificationsOpen(true)} />
        <main className="flex-1 overflow-auto p-6 bg-background/95">
          {children}
        </main>
      </div>
      <NotificationsDrawer
        open={notificationsOpen}
        onClose={() => setNotificationsOpen(false)}
      />
    </div>
  )
}