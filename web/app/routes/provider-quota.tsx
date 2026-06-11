import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  Gauge,
  RefreshCw,
} from "lucide-react"
import { useState } from "react"

import { Badge } from "~/components/ui/badge"
import { Button } from "~/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table"
import { PageHeader } from "~/components/console-layout"
import { type ProviderConnection, useProviderQuota } from "~/lib/api"

export default function ProviderQuota() {
  const { providers, connections, loading, error, reload, refreshQuota } =
    useProviderQuota()
  const [refreshingTarget, setRefreshingTarget] = useState("")
  const activeAccounts = connections.filter((connection) => connection.isActive)
  const limitedAccounts = activeAccounts.filter(
    (connection) => connection.quota?.limited || false
  )
  const checkedAccounts = activeAccounts.filter(
    (connection) => connection.quota?.checkedAt
  )
  const providerGroups = providers
    .map((provider) => ({
      provider,
      accounts: connections.filter(
        (connection) => connection.provider === provider.id
      ),
    }))
    .filter((group) => group.accounts.length > 0)

  async function runRefresh(target: string, input = {}) {
    setRefreshingTarget(target)
    try {
      await refreshQuota(input)
    } finally {
      setRefreshingTarget("")
    }
  }

  return (
    <section className="space-y-6">
      <PageHeader
        eyebrow="Provider Quota"
        title="Provider quota"
        description="Track provider account limits, cooldowns, and last quota checks from upstream responses."
      />
      {error && (
        <Card className="border-destructive/40">
          <CardContent className="py-4 text-sm text-destructive">
            {error}
          </CardContent>
        </Card>
      )}
      <div className="grid gap-4 md:grid-cols-3">
        <Metric
          icon={Gauge}
          label="Active accounts"
          value={loading ? "..." : activeAccounts.length}
          hint="credentials that can route traffic"
        />
        <Metric
          icon={AlertTriangle}
          label="Limited"
          value={loading ? "..." : limitedAccounts.length}
          hint="quota or rate-limit detected"
          tone={limitedAccounts.length > 0 ? "warning" : "muted"}
        />
        <Metric
          icon={Clock}
          label="Checked"
          value={loading ? "..." : checkedAccounts.length}
          hint="accounts with quota telemetry"
        />
      </div>
      <Card>
        <CardHeader>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <CardTitle>Accounts</CardTitle>
              <CardDescription>
                Quota is updated when a model request succeeds or upstream
                returns a quota/rate-limit error.
              </CardDescription>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                className="gap-2"
                onClick={() => void reload()}
                disabled={loading}
              >
                <RefreshCw
                  className={loading ? "size-4 animate-spin" : "size-4"}
                />
                Reload cache
              </Button>
              <Button
                size="sm"
                className="gap-2"
                onClick={() => void runRefresh("all")}
                disabled={loading || refreshingTarget !== ""}
              >
                <RefreshCw
                  className={
                    refreshingTarget === "all"
                      ? "size-4 animate-spin"
                      : "size-4"
                  }
                />
                Check all
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="rounded-lg border p-4 text-sm text-muted-foreground">
              Loading provider quota...
            </div>
          ) : null}
          {!loading && activeAccounts.length === 0 ? (
            <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
              No active provider accounts yet.
            </div>
          ) : null}
          {!loading && providerGroups.length > 0 ? (
            <div className="space-y-4">
              {providerGroups.map(({ provider, accounts }) => (
                <ProviderQuotaGroup
                  key={provider.id}
                  provider={provider}
                  accounts={accounts}
                  loading={loading}
                  refreshingTarget={refreshingTarget}
                  onRefreshProvider={() =>
                    runRefresh(`provider:${provider.id}`, {
                      provider: provider.id,
                    })
                  }
                  onRefreshAccount={(connectionId) =>
                    runRefresh(`account:${connectionId}`, { connectionId })
                  }
                />
              ))}
            </div>
          ) : null}
        </CardContent>
      </Card>
    </section>
  )
}

function ProviderQuotaGroup({
  provider,
  accounts,
  loading,
  refreshingTarget,
  onRefreshProvider,
  onRefreshAccount,
}: {
  provider: { id: string; name: string }
  accounts: ProviderConnection[]
  loading: boolean
  refreshingTarget: string
  onRefreshProvider: () => Promise<void>
  onRefreshAccount: (connectionId: string) => Promise<void>
}) {
  const activeCount = accounts.filter((account) => account.isActive).length
  const providerRefreshing = refreshingTarget === `provider:${provider.id}`
  return (
    <div className="overflow-hidden rounded-lg border">
      <div className="flex flex-col gap-3 border-b bg-muted/30 p-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="font-medium">{provider.name}</div>
          <div className="text-xs text-muted-foreground">
            {activeCount} active / {accounts.length} total accounts
          </div>
        </div>
        <Button
          variant="outline"
          size="sm"
          className="gap-2"
          onClick={() => void onRefreshProvider()}
          disabled={loading || refreshingTarget !== "" || activeCount === 0}
        >
          <RefreshCw
            className={providerRefreshing ? "size-4 animate-spin" : "size-4"}
          />
          Check provider
        </Button>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Account</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Quota</TableHead>
            <TableHead>Reset</TableHead>
            <TableHead>Checked</TableHead>
            <TableHead className="w-24"></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.map((account) => (
            <TableRow key={account.id || `${account.provider}-${account.name}`}>
              <TableCell>
                <div className="font-medium">{account.name}</div>
                <div className="text-xs text-muted-foreground">
                  {account.email || account.displayName || "No email"}
                </div>
              </TableCell>
              <TableCell>
                <QuotaBadge account={account} />
              </TableCell>
              <TableCell>
                <QuotaWindows account={account} />
              </TableCell>
              <TableCell className="text-sm text-muted-foreground">
                {formatDate(account.quota?.resetAt)}
              </TableCell>
              <TableCell className="text-sm text-muted-foreground">
                {formatDate(account.quota?.checkedAt)}
              </TableCell>
              <TableCell className="text-right">
                <Button
                  variant="ghost"
                  size="sm"
                  className="gap-2"
                  onClick={() =>
                    account.id && void onRefreshAccount(account.id)
                  }
                  disabled={
                    loading ||
                    refreshingTarget !== "" ||
                    !account.id ||
                    !account.isActive
                  }
                >
                  <RefreshCw
                    className={
                      account.id && refreshingTarget === `account:${account.id}`
                        ? "size-4 animate-spin"
                        : "size-4"
                    }
                  />
                  Check
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function Metric({
  icon: Icon,
  label,
  value,
  hint,
  tone = "muted",
}: {
  icon: typeof Gauge
  label: string
  value: string | number
  hint: string
  tone?: "muted" | "warning"
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardDescription>{label}</CardDescription>
        <CardTitle className="flex items-center gap-2 text-3xl">
          <Icon
            className={
              tone === "warning"
                ? "size-5 text-amber-600"
                : "size-5 text-muted-foreground"
            }
          />
          {value}
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0 text-sm text-muted-foreground">
        {hint}
      </CardContent>
    </Card>
  )
}

function QuotaBadge({ account }: { account: ProviderConnection }) {
  const plan = account.quota?.plan
  const state = account.quota?.state || "unknown"
  console.debug("[provider-quota] badge", {
    id: account.id,
    provider: account.provider,
    name: account.name,
    status: state,
    plan,
    quota: account.quota,
  })
  if (plan === "paid")
    return (
      <Badge variant="secondary" className="gap-1">
        <CheckCircle2 className="size-3" />
        Paid
      </Badge>
    )
  if (plan === "free")
    return (
      <Badge variant="outline" className="gap-1">
        <CheckCircle2 className="size-3" />
        Free
      </Badge>
    )
  if (state === "limited" || account.quota?.limited)
    return (
      <Badge variant="destructive" className="gap-1">
        <AlertTriangle className="size-3" />
        Limited
      </Badge>
    )
  if (state === "available")
    return (
      <Badge variant="outline" className="gap-1">
        <CheckCircle2 className="size-3" />
        Unknown plan
      </Badge>
    )
  if (state === "error")
    return (
      <Badge variant="outline" className="gap-1">
        <AlertTriangle className="size-3" />
        Error
      </Badge>
    )
  return <Badge variant="outline">Unknown</Badge>
}

function QuotaWindows({ account }: { account: ProviderConnection }) {
  const windows = account.quota?.windows ?? []
  if (windows.length === 0)
    return <span className="text-sm text-muted-foreground">-</span>
  return (
    <div className="grid min-w-72 gap-3 lg:grid-cols-2">
      {windows.map((window) => (
        <QuotaWindow key={window.key} window={window} />
      ))}
    </div>
  )
}

function QuotaWindow({
  window,
}: {
  window: NonNullable<ProviderConnection["quota"]>["windows"][number]
}) {
  const percent = Math.min(100, Math.round(window.percent))
  return (
    <div className="min-w-28 space-y-1">
      <div className="text-xs font-medium text-muted-foreground">
        {window.label}
      </div>
      <div className="flex justify-between text-xs">
        <span>
          {formatQuotaNumber(window.usage)}/{formatQuotaNumber(window.limit)}
        </span>
        <span className="text-muted-foreground">
          {window.exhausted ? "0 left" : `${percent}%`}
        </span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={
            percent >= 100 ? "h-full bg-destructive" : "h-full bg-primary"
          }
          style={{ width: `${percent}%` }}
        />
      </div>
    </div>
  )
}

function formatQuotaNumber(value: number) {
  if (!Number.isFinite(value)) return "0"
  if (Math.abs(value % 1) > 0) return value.toFixed(2)
  return String(value)
}

function formatDate(value?: string) {
  if (!value) return "-"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleString()
}
