"use client"

import {
  Suspense,
  useState,
  ViewTransition
} from "react"

import { Header } from "@/features/shell/header"
import { NotificationsDrawer } from "@/features/notifications/notifications-drawer"
import { ProfileDrawer } from "@/features/users/profile-drawer"

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <Suspense>
      <ViewTransition>
        <Layout>
          {children}
        </Layout>
      </ViewTransition>
    </Suspense>
  )
}

function Layout({
  children,
}: {
  children: React.ReactNode;
}) {
  const [notificationsOpen, setNotificationsOpen] = useState(false)
  const [profileOpen, setProfileOpen] = useState(false)

  return (
    <div className="flex min-h-svh w-full flex-col overflow-visible">
      <Header
        onNotificationsClick={() => setNotificationsOpen(true)}
        onProfileClick={() => setProfileOpen(true)}
      />
      <main className="flex-1 flex flex-col overflow-visible min-h-[calc(100dvh-(--spacing(16)))] h-full bg-background/95 md:p-6 p-4">
        {children}
      </main>
      <NotificationsDrawer
        open={notificationsOpen}
        onClose={() => setNotificationsOpen(false)}
      />
      <ProfileDrawer
        open={profileOpen}
        onClose={() => setProfileOpen(false)}
      />
    </div>
  )
}
