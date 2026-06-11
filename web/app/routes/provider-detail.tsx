import { useEffect, useState } from "react"
import { Link, useParams } from "react-router"
import { CheckCircle2, Copy, ExternalLink, Plus, Trash2 } from "lucide-react"

import { Badge } from "~/components/ui/badge"
import { Button } from "~/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "~/components/ui/dialog"
import { Input } from "~/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "~/components/ui/tabs"
import { FormField, PageHeader } from "~/components/console-layout"
import { type ProviderConnection, useProviderData } from "~/lib/api"

const DEFAULT_CALLBACK_URL = "http://localhost:1455/auth/callback"

export default function ProviderDetail() {
  const { providerId = "" } = useParams()
  const {
    providers,
    connections,
    loading,
    error,
    reload,
    saveConnection,
    deleteConnection,
  } = useProviderData(providerId)
  const provider = providers.find((item) => item.id === providerId)
  const [showAccountForm, setShowAccountForm] = useState(false)
  const [accountForm, setAccountForm] = useState({
    accessToken: "",
    refreshToken: "",
    callbackUrl: DEFAULT_CALLBACK_URL,
    responseUrl: "",
  })
  const [authorizeUrl, setAuthorizeUrl] = useState("")
  const [oauthState, setOauthState] = useState("")
  const [loginError, setLoginError] = useState("")
  const [startingLogin, setStartingLogin] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (
      !showAccountForm ||
      !provider ||
      provider.loginFlow === "import" ||
      authorizeUrl ||
      startingLogin
    )
      return
    void generateBrowserLoginUrl()
  }, [showAccountForm, provider, authorizeUrl, startingLogin])

  function handleAccountDialogChange(open: boolean) {
    if (open) {
      resetAccountForm()
      setShowAccountForm(true)
      return
    }
    resetAccountForm()
    setShowAccountForm(false)
  }

  if (loading)
    return (
      <Card>
        <CardContent className="py-8 text-sm text-muted-foreground">
          Loading provider...
        </CardContent>
      </Card>
    )
  if (error)
    return (
      <Card className="border-destructive/40">
        <CardContent className="py-4 text-sm text-destructive">
          {error}
        </CardContent>
      </Card>
    )
  if (!provider) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Provider not found</CardTitle>
          <CardDescription>
            Select an available provider from the provider grid.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild>
            <Link to="/providers">Back to providers</Link>
          </Button>
        </CardContent>
      </Card>
    )
  }

  const defaultModel = provider.models[0]?.id || ""
  const loginUrl = authorizeUrl || provider.loginUrl || provider.baseUrl
  const canSaveBrowserAccount = Boolean(accountForm.responseUrl)
  const canSaveImportAccount = Boolean(
    accountForm.accessToken && accountForm.refreshToken
  )

  async function generateBrowserLoginUrl() {
    const currentProviderId = provider!.id
    setStartingLogin(true)
    setLoginError("")
    try {
      const response = await fetch(`/api/oauth/${currentProviderId}/start`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          redirectUri: accountForm.callbackUrl || DEFAULT_CALLBACK_URL,
        }),
      })
      if (!response.ok)
        throw new Error(`OAuth start failed with ${response.status}`)
      const data = (await response.json()) as {
        authorizeUrl?: string
        url?: string
        state?: string
      }
      const nextUrl = data.authorizeUrl || data.url || ""
      if (!nextUrl)
        throw new Error("OAuth start did not return an authorize URL")
      setAuthorizeUrl(nextUrl)
      setOauthState(data.state || "")
    } catch (err) {
      setLoginError(
        err instanceof Error ? err.message : "Unable to start browser login"
      )
    } finally {
      setStartingLogin(false)
    }
  }

  function openBrowserLoginUrl() {
    window
      .open(loginUrl, `${provider!.id}-login`, "width=720,height=820")
      ?.focus()
  }

  async function createBrowserAccount() {
    if (!provider) return
    setSaving(true)
    setLoginError("")
    try {
      const response = await fetch(`/api/oauth/${provider.id}/complete`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          responseUrl: accountForm.responseUrl,
          state: oauthState,
        }),
      })
      if (!response.ok) {
        const data = (await response.json().catch(() => null)) as {
          error?: string
        } | null
        throw new Error(
          data?.error || `OAuth completion failed with ${response.status}`
        )
      }
      await reload()
      resetAccountForm()
      setShowAccountForm(false)
    } catch (err) {
      setLoginError(
        err instanceof Error ? err.message : "Unable to complete browser login"
      )
    } finally {
      setSaving(false)
    }
  }

  async function createImportAccount() {
    if (!provider) return
    setSaving(true)
    try {
      const connection: ProviderConnection = {
        provider: provider.id,
        name: `${provider.name} Account`,
        authType: provider.authType || "oauth",
        isActive: true,
        priority: 0,
        defaultModel,
        accessToken: accountForm.accessToken,
        refreshToken: accountForm.refreshToken,
        providerSpecificData: {
          callbackUrl: accountForm.callbackUrl,
          responseUrl: accountForm.responseUrl,
        },
      }
      await saveConnection(connection)
      resetAccountForm()
      setShowAccountForm(false)
    } finally {
      setSaving(false)
    }
  }

  function resetAccountForm() {
    setAccountForm({
      accessToken: "",
      refreshToken: "",
      callbackUrl: DEFAULT_CALLBACK_URL,
      responseUrl: "",
    })
    setAuthorizeUrl("")
    setOauthState("")
    setLoginError("")
  }

  return (
    <section className="space-y-6">
      <PageHeader
        eyebrow="Provider Detail"
        title={provider.name}
        description="Manage accounts and model routing for this provider."
      />
      <Card>
        <CardHeader>
          <div>
            <CardTitle>Accounts</CardTitle>
            <CardDescription>
              Accounts are shown first. Use Add account to open the popup form.
            </CardDescription>
          </div>
          <CardAction>
            <Dialog
              open={showAccountForm}
              onOpenChange={handleAccountDialogChange}
            >
              <DialogTrigger asChild>
                <Button className="gap-2">
                  <Plus className="size-4" />
                  Add account
                </Button>
              </DialogTrigger>
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>Add account</DialogTitle>
                  <DialogDescription>
                    Generate the browser login URL, then import the token or key
                    you get from the provider.
                  </DialogDescription>
                </DialogHeader>
                <Tabs
                  defaultValue={
                    provider.loginFlow === "import" ? "import" : "browser"
                  }
                  className="gap-4"
                >
                  <TabsList>
                    <TabsTrigger value="browser">Browser login</TabsTrigger>
                    <TabsTrigger value="import">Import</TabsTrigger>
                  </TabsList>
                  <TabsContent value="browser" className="space-y-4">
                    <div className="rounded-lg border bg-background p-4">
                      <p className="text-sm font-medium">
                        Step 1: Open this URL in your browser
                      </p>
                      <p className="mt-2 font-mono text-xs break-all text-muted-foreground">
                        {loginUrl}
                      </p>
                      {loginError && (
                        <p className="mt-2 text-sm text-destructive">
                          {loginError}
                        </p>
                      )}
                      <div className="mt-4 flex flex-wrap gap-2">
                        <Button
                          type="button"
                          className="gap-2"
                          onClick={openBrowserLoginUrl}
                          disabled={
                            startingLogin ||
                            provider.loginFlow === "import" ||
                            !loginUrl
                          }
                        >
                          <ExternalLink className="size-4" />
                          {startingLogin ? "Generating..." : "Open"}
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          onClick={() =>
                            void navigator.clipboard.writeText(loginUrl)
                          }
                        >
                          Copy URL
                        </Button>
                      </div>
                    </div>
                    <div className="rounded-lg border bg-background p-4">
                      <FormField label="Step 2: Paste callback URL or authorization code here">
                        <Input
                          value={accountForm.responseUrl}
                          onChange={(event) =>
                            setAccountForm({
                              ...accountForm,
                              responseUrl: event.target.value,
                            })
                          }
                          placeholder="http://localhost:1455/auth/callback?code=...&state=..."
                        />
                      </FormField>
                    </div>
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="outline"
                        onClick={() => handleAccountDialogChange(false)}
                      >
                        Cancel
                      </Button>
                      <Button
                        onClick={createBrowserAccount}
                        disabled={saving || !canSaveBrowserAccount}
                      >
                        {saving ? "Testing..." : "Save account"}
                      </Button>
                    </div>
                  </TabsContent>
                  <TabsContent value="import" className="space-y-4">
                    <div className="grid gap-4 md:grid-cols-2">
                      <FormField label="access_token">
                        <Input
                          value={accountForm.accessToken}
                          onChange={(event) =>
                            setAccountForm({
                              ...accountForm,
                              accessToken: event.target.value,
                            })
                          }
                          placeholder="Paste access_token"
                        />
                      </FormField>
                      <FormField label="refresh_token">
                        <Input
                          value={accountForm.refreshToken}
                          onChange={(event) =>
                            setAccountForm({
                              ...accountForm,
                              refreshToken: event.target.value,
                            })
                          }
                          placeholder="Paste refresh_token"
                        />
                      </FormField>
                    </div>
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="outline"
                        onClick={() => handleAccountDialogChange(false)}
                      >
                        Cancel
                      </Button>
                      <Button
                        onClick={createImportAccount}
                        disabled={saving || !canSaveImportAccount}
                      >
                        {saving ? "Saving..." : "Save account"}
                      </Button>
                    </div>
                  </TabsContent>
                </Tabs>
              </DialogContent>
            </Dialog>
          </CardAction>
        </CardHeader>
        <CardContent className="space-y-4">
          {connections.length === 0 ? (
            <EmptyState />
          ) : (
            connections.map((account) => (
              <div
                key={account.id}
                className="flex flex-col gap-3 rounded-xl border p-4 md:flex-row md:items-center md:justify-between"
              >
                <div>
                  <p className="font-medium">{account.name}</p>
                  <p className="text-sm text-muted-foreground">
                    {account.email || "No email"} · priority {account.priority}
                  </p>
                </div>
                {account.id && (
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => deleteConnection(account.id!)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                )}
              </div>
            ))
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Models</CardTitle>
          <CardDescription>
            Model identifiers are shown below accounts using provider/model
            convention.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            {provider.models.map((model) => (
              <div
                key={model.id}
                className="flex items-center justify-between rounded-lg border bg-background p-3"
              >
                <code className="text-xs">
                  {provider.id}/{model.id}
                </code>
                <Copy className="size-3.5 text-muted-foreground" />
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </section>
  )
}

function EmptyState() {
  return (
    <div className="rounded-xl border border-dashed p-8 text-center">
      <CheckCircle2 className="mx-auto size-8 text-muted-foreground" />
      <p className="mt-3 font-medium">No accounts yet</p>
      <p className="text-sm text-muted-foreground">
        Use Add account to configure credentials for this provider.
      </p>
    </div>
  )
}
