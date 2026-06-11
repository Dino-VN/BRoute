import { useState } from "react"
import { KeyRound, Plus, Trash2 } from "lucide-react"

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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog"
import { Input } from "~/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table"
import { FormField, PageHeader } from "~/components/console-layout"
import { useAPIKeys, useModelData } from "~/lib/api"

export default function ApiManagement() {
  const { models, loading, error } = useModelData()
  const apiKeys = useAPIKeys()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [keyForm, setKeyForm] = useState({
    name: "",
    limitModels: false,
    allowedModels: [] as string[],
  })
  const [createdKey, setCreatedKey] = useState("")
  const [message, setMessage] = useState("")

  async function createApiKey() {
    setMessage("")
    setCreatedKey("")
    const created = await apiKeys.createKey({
      name: keyForm.name || "Gateway key",
      allowedModels: keyForm.limitModels ? keyForm.allowedModels : [],
    })
    setCreatedKey(created.key ?? "")
    setKeyForm({ name: "", limitModels: false, allowedModels: [] })
    setDialogOpen(false)
  }

  function toggleModel(model: string) {
    setKeyForm((current) => {
      const exists = current.allowedModels.includes(model)
      return {
        ...current,
        allowedModels: exists
          ? current.allowedModels.filter((item) => item !== model)
          : [...current.allowedModels, model],
      }
    })
  }

  async function deleteKey(id: string) {
    setMessage("")
    await apiKeys.deleteKey(id)
    setMessage("API key deleted.")
  }

  return (
    <section className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <PageHeader
          eyebrow="API Management"
          title="API keys"
          description="Create gateway keys that can use every available model by default, or limit them to selected models."
        />
        <Button
          onClick={() => setDialogOpen(true)}
          className="gap-2 sm:self-center"
        >
          <Plus className="size-4" />
          Add API key
        </Button>
      </div>
      {error && (
        <Card className="border-destructive/40">
          <CardContent className="py-4 text-sm text-destructive">
            {error}
          </CardContent>
        </Card>
      )}
      {apiKeys.error && (
        <Card className="border-destructive/40">
          <CardContent className="py-4 text-sm text-destructive">
            {apiKeys.error}
          </CardContent>
        </Card>
      )}
      {message && (
        <Card>
          <CardContent className="py-4 text-sm text-muted-foreground">
            {message}
          </CardContent>
        </Card>
      )}
      {createdKey && (
        <Card>
          <CardContent className="space-y-2 py-4">
            <div className="text-sm font-medium">New key created</div>
            <code className="block overflow-x-auto rounded-md bg-muted p-3 text-xs">
              {createdKey}
            </code>
          </CardContent>
        </Card>
      )}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Add API key</DialogTitle>
            <DialogDescription>
              The key value is generated after you save.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <FormField label="Key name">
              <Input
                value={keyForm.name}
                onChange={(event) =>
                  setKeyForm({ ...keyForm, name: event.target.value })
                }
                placeholder="Production app"
              />
            </FormField>
            <label className="flex items-center gap-2 text-sm font-medium">
              <input
                type="checkbox"
                checked={keyForm.limitModels}
                onChange={(event) =>
                  setKeyForm({ ...keyForm, limitModels: event.target.checked })
                }
              />
              Limit this key to selected models
            </label>
            {keyForm.limitModels && (
              <div className="grid max-h-72 gap-2 overflow-auto rounded-md border p-3 md:grid-cols-2">
                {loading && (
                  <div className="text-sm text-muted-foreground">
                    Loading models...
                  </div>
                )}
                {!loading && models.length === 0 && (
                  <div className="text-sm text-muted-foreground">
                    No models available. Add an active provider account first.
                  </div>
                )}
                {models.map((model) => (
                  <label
                    key={model}
                    className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted"
                  >
                    <input
                      type="checkbox"
                      checked={keyForm.allowedModels.includes(model)}
                      onChange={() => toggleModel(model)}
                    />
                    <span className="truncate">{model}</span>
                  </label>
                ))}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => void createApiKey()}
              disabled={
                !keyForm.name.trim() ||
                (keyForm.limitModels && keyForm.allowedModels.length === 0)
              }
              className="gap-2"
            >
              <Plus className="size-4" />
              Create
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Card>
        <CardHeader>
          <CardTitle>API keys</CardTitle>
          <CardDescription>
            Keys with no model restriction can use all available models.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {apiKeys.loading ? (
            <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
              Loading API keys...
            </div>
          ) : apiKeys.keys.length === 0 ? (
            <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
              No API keys created yet.
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Key</TableHead>
                  <TableHead>Models</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {apiKeys.keys.map((key) => (
                  <TableRow key={key.id}>
                    <TableCell className="font-medium">{key.name}</TableCell>
                    <TableCell>
                      <code className="text-xs">{key.key}</code>
                    </TableCell>
                    <TableCell>
                      {key.allowedModels.length === 0 ? (
                        <Badge variant="secondary" className="gap-1">
                          <KeyRound className="size-3" />
                          All models
                        </Badge>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {key.allowedModels.map((model) => (
                            <Badge key={model} variant="outline">
                              {model}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">
                        {key.isActive ? "Active" : "Disabled"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => void deleteKey(key.id)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </section>
  )
}
