import { useState } from "react"
import { Plus } from "lucide-react"

import { Badge } from "~/components/ui/badge"
import { Button } from "~/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card"
import { Input } from "~/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "~/components/ui/table"
import { FormField, PageHeader } from "~/components/console-layout"
import { useModelData } from "~/lib/api"

type LocalGeneratedKey = {
  id: string
  name: string
  key: string
  scope: string
  limit: string
  status: string
}

export default function ApiManagement() {
  const { models, loading, error } = useModelData()
  const [keys, setKeys] = useState<LocalGeneratedKey[]>([])
  const [keyForm, setKeyForm] = useState({ name: "", scope: "", limit: "60 rpm" })
  const selectedScope = keyForm.scope || models[0] || ""

  function createApiKey() {
    const suffix = crypto.getRandomValues(new Uint32Array(2)).join("")
    setKeys((current) => [
      {
        id: crypto.randomUUID(),
        name: keyForm.name || "Generated key",
        key: `or_live_sk_${suffix}`,
        scope: selectedScope,
        limit: keyForm.limit,
        status: "Generated",
      },
      ...current,
    ])
    setKeyForm((current) => ({ ...current, name: "" }))
  }

  return (
    <section className="space-y-6">
      <PageHeader eyebrow="API Management" title="Generate and configure API keys" description="Create gateway keys and bind them to real provider/model identifiers from the backend." />
      {error && <Card className="border-destructive/40"><CardContent className="py-4 text-sm text-destructive">{error}</CardContent></Card>}
      <Card>
        <CardHeader>
          <CardTitle>New API key</CardTitle>
          <CardDescription>Model scopes are loaded from /api/v1/models.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 lg:grid-cols-[1fr_1fr_180px_auto] lg:items-end">
          <FormField label="Key name"><Input value={keyForm.name} onChange={(event) => setKeyForm({ ...keyForm, name: event.target.value })} placeholder="Production app" /></FormField>
          <FormField label="Model scope">
            <Select value={selectedScope} onValueChange={(scope) => setKeyForm({ ...keyForm, scope })} disabled={loading || models.length === 0}>
              <SelectTrigger><SelectValue placeholder={loading ? "Loading models..." : "Select model"} /></SelectTrigger>
              <SelectContent>{models.map((model) => <SelectItem key={model} value={model}>{model}</SelectItem>)}</SelectContent>
            </Select>
          </FormField>
          <FormField label="Rate limit"><Input value={keyForm.limit} onChange={(event) => setKeyForm({ ...keyForm, limit: event.target.value })} /></FormField>
          <Button onClick={createApiKey} disabled={!selectedScope} className="gap-2"><Plus className="size-4" />Generate</Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>API keys</CardTitle>
          <CardDescription>Generated keys in this session. Add backend persistence when an API-key store is available.</CardDescription>
        </CardHeader>
        <CardContent>
          {keys.length === 0 ? <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">No API keys generated yet.</div> : (
            <Table>
              <TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Key</TableHead><TableHead>Scope</TableHead><TableHead>Limit</TableHead><TableHead>Status</TableHead></TableRow></TableHeader>
              <TableBody>{keys.map((key) => <TableRow key={key.id}><TableCell className="font-medium">{key.name}</TableCell><TableCell><code className="text-xs">{key.key}</code></TableCell><TableCell>{key.scope}</TableCell><TableCell>{key.limit}</TableCell><TableCell><Badge variant="secondary">{key.status}</Badge></TableCell></TableRow>)}</TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </section>
  )
}
