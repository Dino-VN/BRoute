import { useEffect } from "react"
import type { ComponentType, ReactNode } from "react"
import {
  Download,
  Gauge,
  HomeIcon,
  KeyRound,
  LogOut,
  RefreshCw,
  ScrollText,
  ServerCog,
} from "lucide-react"
import { NavLink, Outlet, useLocation, useNavigate } from "react-router"

import { Badge } from "~/components/ui/badge"
import { Button } from "~/components/ui/button"
import { Card, CardContent } from "~/components/ui/card"
import { logout, useAuthStatus } from "~/lib/auth"
import { useUpdateStatus } from "~/lib/api"

const navItems = [
  { to: "/", label: "Home", icon: HomeIcon },
  { to: "/api", label: "API Management", icon: KeyRound },
  { to: "/providers", label: "Providers", icon: ServerCog },
  { to: "/quota", label: "Provider Quota", icon: Gauge },
  { to: "/debug-logs", label: "Debug Logs", icon: ScrollText },
]

export default function ConsoleLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const { settings, loading, error } = useAuthStatus()
  const updateStatus = useUpdateStatus()
  const needsAuth = settings?.requireLogin && !settings.authenticated

  useEffect(() => {
    if (!loading && !error && needsAuth && location.pathname !== "/login") {
      navigate(
        `/login?redirect=${encodeURIComponent(location.pathname + location.search)}`,
        { replace: true }
      )
    }
  }, [error, loading, location.pathname, location.search, navigate, needsAuth])

  if (loading) {
    return <FullScreenMessage message="Checking authentication..." />
  }

  if (error) {
    return <FullScreenMessage message={error} tone="error" />
  }

  if (needsAuth) {
    return null
  }

  async function handleLogout() {
    await logout()
    navigate("/login", { replace: true })
  }

  return (
    <div className="min-h-svh bg-muted/30 text-foreground">
      <div className="grid min-h-svh lg:grid-cols-[280px_1fr]">
        <aside className="hidden min-h-svh flex-col border-r bg-background/95 p-6 lg:flex">
          <div>
            <div className="flex items-center justify-between gap-3">
              <Brand />
              {settings?.requireLogin && (
                <Button variant="ghost" size="icon" onClick={handleLogout}>
                  <LogOut className="size-4" />
                </Button>
              )}
            </div>
            <nav className="mt-8 grid gap-2">
              {navItems.map((item) => (
                <ConsoleNavLink key={item.to} {...item} />
              ))}
            </nav>
          </div>
          <div className="mt-auto pt-6">
            <UpdatePanel {...updateStatus} />
          </div>
        </aside>

        <main className="min-w-0 p-4 sm:p-6 lg:p-8">
          <div className="mb-6 flex flex-col gap-4 rounded-2xl border bg-background p-4 shadow-sm lg:hidden">
            <div className="flex items-center justify-between gap-3">
              <Brand />
              {settings?.requireLogin && (
                <Button variant="ghost" size="icon" onClick={handleLogout}>
                  <LogOut className="size-4" />
                </Button>
              )}
            </div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              {navItems.map((item) => (
                <MobileNavLink key={item.to} {...item} />
              ))}
            </div>
            <UpdatePanel {...updateStatus} compact />
          </div>
          <Outlet />
        </main>
      </div>
    </div>
  )
}

function UpdatePanel({
  status,
  loading,
  updating,
  message,
  update,
  compact = false,
}: ReturnType<typeof useUpdateStatus> & { compact?: boolean }) {
  const current = status?.currentVersion || "0.0.1"
  const latest = status?.latestVersion || ""
  const available = Boolean(status?.updateAvailable)
  const visible =
    available || loading || Boolean(status?.error) || Boolean(message)

  if (!visible) return null

  return (
    <div
      className={
        compact
          ? "rounded-lg border bg-muted/30 p-3"
          : "rounded-lg border bg-muted/30 p-4"
      }
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs font-medium text-muted-foreground uppercase">
            Version
          </p>
          <p className="mt-1 truncate text-sm font-semibold">v{current}</p>
        </div>
        {available ? <Badge>v{latest}</Badge> : null}
      </div>
      {available ? (
        <Button
          className="mt-3 w-full gap-2"
          size="sm"
          onClick={update}
          disabled={updating}
        >
          {updating ? (
            <RefreshCw className="size-4 animate-spin" />
          ) : (
            <Download className="size-4" />
          )}
          {updating ? "Updating" : "Update"}
        </Button>
      ) : (
        <p className="mt-3 text-xs text-muted-foreground">
          {loading ? "Checking for updates..." : status?.error}
        </p>
      )}
      {message && (
        <p className="mt-2 text-xs text-muted-foreground">{message}</p>
      )}
    </div>
  )
}

export function Brand() {
  return (
    <div className="flex items-center gap-3">
      <div className="grid size-11 place-items-center rounded-xl bg-primary text-sm font-black text-primary-foreground">
        OR
      </div>
      <div>
        <p className="text-sm leading-none font-semibold">BRoute</p>
      </div>
    </div>
  )
}

function ConsoleNavLink({
  to,
  label,
  icon: Icon,
}: {
  to: string
  label: string
  icon: ComponentType<{ className?: string }>
}) {
  return (
    <Button
      asChild
      variant="ghost"
      className="justify-start gap-2 has-[.active]:bg-secondary"
    >
      <NavLink to={to} end={to === "/"}>
        {({ isActive }) => (
          <>
            <Icon className="size-4" />
            <span className={isActive ? "font-semibold" : undefined}>
              {label}
            </span>
          </>
        )}
      </NavLink>
    </Button>
  )
}

function MobileNavLink({
  to,
  label,
}: {
  to: string
  label: string
  icon: ComponentType<{ className?: string }>
}) {
  return (
    <Button asChild variant="outline">
      <NavLink to={to} end={to === "/"}>
        {label.replace(" Management", "")}
      </NavLink>
    </Button>
  )
}

function FullScreenMessage({
  message,
  tone = "muted",
}: {
  message: string
  tone?: "muted" | "error"
}) {
  return (
    <div className="grid min-h-svh place-items-center bg-muted/30 p-6">
      <Card className="w-full max-w-sm">
        <CardContent
          className={
            tone === "error"
              ? "py-6 text-sm text-destructive"
              : "py-6 text-sm text-muted-foreground"
          }
        >
          {message}
        </CardContent>
      </Card>
    </div>
  )
}

export function PageHeader({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string
  title: string
  description: string
}) {
  return (
    <div className="space-y-2">
      <Badge variant="outline">{eyebrow}</Badge>
      <h1 className="max-w-4xl text-3xl font-semibold tracking-tight sm:text-4xl">
        {title}
      </h1>
      <p className="max-w-2xl text-muted-foreground">{description}</p>
    </div>
  )
}

export function FormField({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="grid gap-2">
      <label className="text-sm leading-none font-medium">{label}</label>
      {children}
    </div>
  )
}

export function CodeLine({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-lg bg-muted px-3 py-2 font-mono text-xs">
      {children}
    </div>
  )
}
