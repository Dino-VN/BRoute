import { Link } from "react-router"
import { ArrowRight } from "lucide-react"

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
import { useProviderData } from "~/lib/api"

export default function Providers() {
  const { providers, connections, loading, error } = useProviderData()

  return (
    <section className="space-y-6">
      <PageHeader
        eyebrow="Provider Management"
        title="Providers"
        description="Select a provider block to open its account and model management view."
      />
      {error && (
        <Card className="border-destructive/40">
          <CardContent className="py-4 text-sm text-destructive">
            {error}
          </CardContent>
        </Card>
      )}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {loading && (
          <Card>
            <CardContent className="py-8 text-sm text-muted-foreground">
              Loading providers...
            </CardContent>
          </Card>
        )}
        {providers.map((provider) => {
          const count = connections.filter(
            (account) => account.provider === provider.id
          ).length
          return (
            <Card
              key={provider.id}
              className="transition hover:-translate-y-0.5 hover:shadow-md"
            >
              <CardHeader>
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <CardTitle>{provider.name}</CardTitle>
                    <CardDescription>{provider.id}</CardDescription>
                  </div>
                  <Badge variant="outline">{count} acc</Badge>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="text-sm text-muted-foreground">
                  {provider.models.length} models
                </div>
                <Button asChild className="w-full justify-between">
                  <Link to={`/providers/${provider.id}`}>
                    Manage provider <ArrowRight className="size-4" />
                  </Link>
                </Button>
              </CardContent>
            </Card>
          )
        })}
      </div>
    </section>
  )
}
