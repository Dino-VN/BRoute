import { RefreshCw, Trash2 } from "lucide-react"

import { Badge } from "~/components/ui/badge"
import { Button } from "~/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card"
import { PageHeader } from "~/components/console-layout"
import { type DebugLogEntry, useDebugLogs } from "~/lib/api"

export default function DebugLogs() {
  const { logs, loading, error, reload, clear } = useDebugLogs()

  return (
    <section className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <PageHeader
          eyebrow="Debug"
          title="Request logs"
          description="Inspect gateway requests, converted provider bodies, and upstream errors."
        />
        <div className="flex gap-2 sm:self-center">
          <Button variant="outline" className="gap-2" onClick={() => void reload()}>
            <RefreshCw className="size-4" />
            Reload
          </Button>
          <Button variant="outline" className="gap-2" onClick={() => void clear()}>
            <Trash2 className="size-4" />
            Clear
          </Button>
        </div>
      </div>
      {error && (
        <Card className="border-destructive/40">
          <CardContent className="py-4 text-sm text-destructive">
            {error}
          </CardContent>
        </Card>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Recent requests</CardTitle>
          <CardDescription>
            Logs are kept in memory and reset when the server restarts.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {loading ? (
            <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
              Loading debug logs...
            </div>
          ) : logs.length === 0 ? (
            <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
              No requests logged yet.
            </div>
          ) : (
            logs.map((entry) => <DebugLogCard key={entry.id} entry={entry} />)
          )}
        </CardContent>
      </Card>
    </section>
  )
}

function DebugLogCard({ entry }: { entry: DebugLogEntry }) {
  const tone = entry.status === "ok" ? "secondary" : "destructive"
  return (
    <div className="space-y-4 rounded-lg border bg-background p-4">
      <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
        <div className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={tone}>{entry.status}</Badge>
            <span className="font-mono text-xs text-muted-foreground">
              {entry.method} {entry.path}
            </span>
          </div>
          <div className="text-sm text-muted-foreground">
            {new Date(entry.createdAt).toLocaleString()} · {entry.durationMs}ms
            {entry.httpStatus ? ` · HTTP ${entry.httpStatus}` : ""}
          </div>
          <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
            {entry.provider && <span>provider: {entry.provider}</span>}
            {entry.model && <span>model: {entry.model}</span>}
            {entry.accountName && <span>account: {entry.accountName}</span>}
            {entry.connectionId && <span>connection: {entry.connectionId}</span>}
            {entry.stream && <span>stream</span>}
          </div>
        </div>
      </div>
      {entry.error && (
        <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
          {entry.error}
        </div>
      )}
      {entry.upstreamUrl && (
        <div className="font-mono text-xs text-muted-foreground break-all">
          upstream: {entry.upstreamUrl}
          {entry.upstreamStatus ? ` · ${entry.upstreamStatus}` : ""}
        </div>
      )}
      <div className="grid gap-3 xl:grid-cols-2">
        <JsonBlock title="Original body" value={entry.originalBody} />
        <JsonBlock title="Converted body" value={entry.convertedBody} />
      </div>
      {entry.toolCallDump && (
        <JsonBlock title="Tool call dump" value={entry.toolCallDump} />
      )}
      {entry.upstreamBody && (
        <JsonBlock title="Upstream body" value={entry.upstreamBody} />
      )}
    </div>
  )
}

function JsonBlock({ title, value }: { title: string; value: unknown }) {
  if (value === undefined || value === null || value === "") return null
  const content = typeof value === "string" ? value : JSON.stringify(value, null, 2)
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium">{title}</div>
      <pre className="max-h-96 overflow-auto rounded-md bg-muted p-3 text-xs leading-relaxed">
        {content}
      </pre>
    </div>
  )
}
