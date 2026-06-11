import { Activity, Boxes, Router, ServerCog, ShieldCheck } from "lucide-react"

import { Badge } from "~/components/ui/badge"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card"
import { Separator } from "~/components/ui/separator"
import { CodeLine, PageHeader } from "~/components/console-layout"
import { useModelData, useProviderData } from "~/lib/api"

export default function Home() {
  const { providers, connections, loading, error } = useProviderData()
  const { models, error: modelsError } = useModelData()

  return (
    <section className="space-y-6">
      <PageHeader
        eyebrow="Dashboard"
        title="Codex backend control plane"
        description="A shadcn-based interface for monitoring provider coverage, gateway credentials, and provider/model routing."
      />
      {(error || modelsError) && <ErrorCard message={error || modelsError} />}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Metric
          icon={ServerCog}
          label="Providers"
          value={loading ? "..." : providers.length}
          hint="configured upstreams"
        />
        <Metric
          icon={ShieldCheck}
          label="Accounts"
          value={connections.length}
          hint="stored credentials"
        />
        <Metric
          icon={Boxes}
          label="Models"
          value={
            models.length ||
            providers.reduce(
              (total, provider) => total + provider.models.length,
              0
            )
          }
          hint="provider/model targets"
        />
        <Metric icon={Router} label="API" value="v1" hint="OpenAI compatible" />
      </div>
      <div className="grid gap-4 xl:grid-cols-[1.4fr_0.8fr]">
        <Card>
          <CardHeader>
            <CardTitle>Routing health</CardTitle>
            <CardDescription>
              High-level status for gateway traffic and provider/model naming.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {providers.map((provider) => (
              <div
                key={provider.id}
                className="flex items-center justify-between rounded-xl border p-4"
              >
                <div className="flex items-center gap-3">
                  <div className="rounded-lg bg-muted p-2">
                    <Activity className="size-4" />
                  </div>
                  <div>
                    <p className="font-medium">{provider.name}</p>
                    <p className="text-sm text-muted-foreground">
                      {
                        connections.filter(
                          (connection) => connection.provider === provider.id
                        ).length
                      }{" "}
                      accounts
                    </p>
                  </div>
                </div>
                <Badge variant="secondary">
                  {provider.id}/{provider.models[0]?.id ?? "model"}
                </Badge>
              </div>
            ))}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Usage pattern</CardTitle>
            <CardDescription>
              Clients can call the backend using OpenAI compatible endpoints.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <CodeLine>/api/v1/chat/completions</CodeLine>
            <CodeLine>/api/v1/responses</CodeLine>
            <Separator />
            <p className="text-muted-foreground">
              Set request model to{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                provider/model
              </code>
              , for example{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                codex/gpt-5.5
              </code>
              .
            </p>
          </CardContent>
        </Card>
      </div>
    </section>
  )
}

function Metric({
  icon: Icon,
  label,
  value,
  hint,
}: {
  icon: typeof ServerCog
  label: string
  value: string | number
  hint: string
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardDescription>{label}</CardDescription>
        <CardAction>
          <Icon className="size-4 text-muted-foreground" />
        </CardAction>
        <CardTitle className="text-3xl">{value}</CardTitle>
      </CardHeader>
      <CardContent className="pt-0 text-sm text-muted-foreground">
        {hint}
      </CardContent>
    </Card>
  )
}

function ErrorCard({ message }: { message: string }) {
  return (
    <Card className="border-destructive/40">
      <CardContent className="py-4 text-sm text-destructive">
        {message}
      </CardContent>
    </Card>
  )
}
