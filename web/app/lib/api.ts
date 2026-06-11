import { useEffect, useState } from "react"

export type ProviderModel = {
  id: string
  name: string
  provider: string
}

export type Provider = {
  id: string
  name: string
  format: string
  authType: string
  authHeader: string
  baseUrl: string
  loginUrl?: string
  loginFlow?: "browser" | "import" | string
  models: ProviderModel[]
}

export type ProviderConnection = {
  id?: string
  provider: string
  name: string
  email?: string
  displayName?: string
  authType: string
  isActive: boolean
  priority: number
  defaultModel: string
  rateLimitProtection?: boolean
  accessToken?: string
  refreshToken?: string
  apiKey?: string
  projectId?: string
  quotaStatus?: "available" | "limited" | "error" | string
  quotaDetail?: string
  quotaResetAt?: string
  quotaCheckedAt?: string
  quota?: ProviderQuota
  quotaData?: Record<string, unknown>
  providerSpecificData?: Record<string, unknown>
  createdAt?: string
  updatedAt?: string
}

export type ProviderQuota = {
  provider: string
  state: "available" | "limited" | "error" | "unknown" | string
  plan?: "free" | "paid" | string
  limited: boolean
  resetAt?: string
  checkedAt?: string
  windows: ProviderQuotaWindow[]
}

export type ProviderQuotaWindow = {
  key: "fiveHour" | "sevenDay" | "thirtyDay" | string
  label: string
  usage: number
  limit: number
  remaining: number
  percent: number
  exhausted: boolean
  resetAt?: string
}

type ProvidersResponse = {
  providers?: Provider[]
  connections?: ProviderConnection[]
}

type ModelsResponse = {
  data?: Array<{ id: string; owned_by: string }>
}

export type UpdateStatus = {
  currentVersion: string
  latestVersion: string
  updateAvailable: boolean
  error?: string
}

export type GatewayAPIKey = {
  id: string
  name: string
  key?: string
  allowedModels: string[]
  isActive: boolean
  createdAt: string
  updatedAt: string
}

type APIKeysResponse = {
  keys?: GatewayAPIKey[]
}

export type DebugLogEntry = {
  id: string
  createdAt: string
  method: string
  path: string
  provider?: string
  model?: string
  connectionId?: string
  accountName?: string
  stream: boolean
  status: "ok" | "error" | string
  httpStatus?: number
  durationMs: number
  error?: string
  originalBody?: Record<string, unknown>
  convertedBody?: unknown
  toolCallDump?: string
  upstreamUrl?: string
  upstreamStatus?: number
  upstreamBody?: string
}

type DebugLogsResponse = {
  logs?: DebugLogEntry[]
}

export function useProviderData(providerId?: string) {
  const [providers, setProviders] = useState<Provider[]>([])
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  async function load() {
    setLoading(true)
    try {
      const suffix = providerId
        ? `?provider=${encodeURIComponent(providerId)}`
        : ""
      const response = await fetch(`/api/providers${suffix}`, {
        credentials: "include",
      })
      if (!response.ok)
        throw new Error(`Provider API returned ${response.status}`)
      const data = (await response.json()) as ProvidersResponse
      setProviders(data.providers ?? [])
      setConnections(data.connections ?? [])
      setError("")
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Unable to load provider data"
      )
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [providerId])

  async function saveConnection(connection: ProviderConnection) {
    const response = await fetch("/api/providers", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(connection),
    })
    if (!response.ok) throw new Error(`Save failed with ${response.status}`)
    await load()
  }

  async function deleteConnection(id: string) {
    const response = await fetch(`/api/providers/${id}`, {
      method: "DELETE",
      credentials: "include",
    })
    if (!response.ok) throw new Error(`Delete failed with ${response.status}`)
    await load()
  }

  return {
    providers,
    connections,
    loading,
    error,
    reload: load,
    saveConnection,
    deleteConnection,
  }
}

export function useProviderQuota(providerId?: string) {
  const [providers, setProviders] = useState<Provider[]>([])
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  async function load() {
    setLoading(true)
    try {
      const suffix = providerId
        ? `?provider=${encodeURIComponent(providerId)}`
        : ""
      const response = await fetch(`/api/provider-quota${suffix}`, {
        credentials: "include",
      })
      if (!response.ok)
        throw new Error(`Provider quota returned ${response.status}`)
      const data = (await response.json()) as ProvidersResponse
      console.debug("[provider-quota] load", {
        providerId,
        connections: quotaDebugConnections(data.connections ?? []),
      })
      setProviders(data.providers ?? [])
      setConnections(data.connections ?? [])
      setError("")
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Unable to load provider quota"
      )
    } finally {
      setLoading(false)
    }
  }

  async function refreshQuota(
    input: { provider?: string; connectionId?: string } = {}
  ) {
    try {
      console.debug("[provider-quota] refresh:start", input)
      const response = await fetch("/api/provider-quota", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      })
      if (!response.ok)
        throw new Error(`Provider quota refresh returned ${response.status}`)
      const data = (await response.json()) as ProvidersResponse
      console.debug("[provider-quota] refresh:response", {
        input,
        connections: quotaDebugConnections(data.connections ?? []),
      })
      setProviders(data.providers ?? [])
      setConnections((current) =>
        mergeQuotaConnections(current, data.connections ?? [], input)
      )
      setError("")
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Unable to refresh provider quota"
      )
    }
  }

  useEffect(() => {
    void load()
  }, [providerId])

  return { providers, connections, loading, error, reload: load, refreshQuota }
}

function quotaDebugConnections(connections: ProviderConnection[]) {
  return connections.map((connection) => ({
    id: connection.id,
    provider: connection.provider,
    name: connection.name,
    state: connection.quota?.state,
    plan: connection.quota?.plan,
    checkedAt: connection.quota?.checkedAt,
    windows: connection.quota?.windows,
  }))
}

function mergeQuotaConnections(
  current: ProviderConnection[],
  refreshed: ProviderConnection[],
  input: { provider?: string; connectionId?: string }
) {
  if (!input.provider && !input.connectionId) return refreshed
  const refreshedById = new Map(
    refreshed
      .filter((connection) => connection.id)
      .map((connection) => [connection.id, connection])
  )
  if (input.connectionId) {
    return current.map((connection) =>
      connection.id && refreshedById.has(connection.id)
        ? refreshedById.get(connection.id)!
        : connection
    )
  }
  if (input.provider) {
    return current.map((connection) =>
      connection.provider === input.provider &&
      connection.id &&
      refreshedById.has(connection.id)
        ? refreshedById.get(connection.id)!
        : connection
    )
  }
  return current
}

export function useModelData() {
  const [models, setModels] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  useEffect(() => {
    let cancelled = false

    async function load() {
      try {
        const response = await fetch("/api/v1/models", {
          credentials: "include",
        })
        if (!response.ok)
          throw new Error(`Models API returned ${response.status}`)
        const data = (await response.json()) as ModelsResponse
        if (!cancelled) {
          setModels((data.data ?? []).map((model) => model.id))
          setError("")
        }
      } catch (err) {
        if (!cancelled)
          setError(err instanceof Error ? err.message : "Unable to load models")
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [])

  return { models, loading, error }
}

export function useAPIKeys() {
  const [keys, setKeys] = useState<GatewayAPIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  async function load() {
    setLoading(true)
    try {
      const response = await fetch("/api/api-keys", { credentials: "include" })
      if (!response.ok) throw new Error(`API keys returned ${response.status}`)
      const data = (await response.json()) as APIKeysResponse
      setKeys(data.keys ?? [])
      setError("")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load API keys")
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function createKey(input: { name: string; allowedModels: string[] }) {
    const response = await fetch("/api/api-keys", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    })
    if (!response.ok) throw new Error(`Create failed with ${response.status}`)
    const created = (await response.json()) as GatewayAPIKey
    await load()
    return created
  }

  async function deleteKey(id: string) {
    const response = await fetch(`/api/api-keys/${id}`, {
      method: "DELETE",
      credentials: "include",
    })
    if (!response.ok) throw new Error(`Delete failed with ${response.status}`)
    await load()
  }

  return { keys, loading, error, reload: load, createKey, deleteKey }
}

export function useUpdateStatus() {
  const [status, setStatus] = useState<UpdateStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [updating, setUpdating] = useState(false)
  const [message, setMessage] = useState("")

  async function load() {
    setLoading(true)
    try {
      const response = await fetch("/api/update", { credentials: "include" })
      if (!response.ok)
        throw new Error(`Update API returned ${response.status}`)
      setStatus((await response.json()) as UpdateStatus)
    } catch (err) {
      setStatus(null)
      setMessage(
        err instanceof Error ? err.message : "Unable to check for updates"
      )
    } finally {
      setLoading(false)
    }
  }

  async function update() {
    setUpdating(true)
    setMessage("")
    try {
      const currentVersion = status?.currentVersion
      const response = await fetch("/api/update", {
        method: "POST",
        credentials: "include",
      })
      const data = (await response.json()) as {
        success?: boolean
        error?: string
        restartRequired?: boolean
        restarting?: boolean
        latestVersion?: string
      }
      if (!response.ok || !data.success)
        throw new Error(data.error || `Update failed with ${response.status}`)
      if (data.restarting) {
        setMessage("Update installed. Restarting...")
        await waitForRestart(data.latestVersion || currentVersion)
        window.location.reload()
        return
      }
      setMessage("Update installed.")
      await load()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Unable to update")
    } finally {
      setUpdating(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return { status, loading, updating, message, reload: load, update }
}

async function waitForRestart(targetVersion?: string) {
  const deadline = Date.now() + 120_000
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`/api/health/ping?t=${Date.now()}`, {
        cache: "no-store",
      })
      if (response.ok) {
        const data = (await response.json()) as { ok?: boolean; version?: string }
        const version = data.version?.replace(/^v/, "")
        const target = targetVersion?.replace(/^v/, "")
        if (data.ok && (!target || !version || version === target)) return
      }
    } catch {
      // Server is expected to disappear briefly while the process restarts.
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000))
  }
  throw new Error("Update installed, but the server did not come back online in time.")
}

export function useDebugLogs() {
  const [logs, setLogs] = useState<DebugLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  async function load() {
    setLoading(true)
    try {
      const response = await fetch("/api/debug-logs", { credentials: "include" })
      if (!response.ok)
        throw new Error(`Debug logs returned ${response.status}`)
      const data = (await response.json()) as DebugLogsResponse
      setLogs(data.logs ?? [])
      setError("")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load debug logs")
    } finally {
      setLoading(false)
    }
  }

  async function clear() {
    const response = await fetch("/api/debug-logs", {
      method: "DELETE",
      credentials: "include",
    })
    if (!response.ok) throw new Error(`Clear logs failed with ${response.status}`)
    await load()
  }

  useEffect(() => {
    void load()
  }, [])

  return { logs, loading, error, reload: load, clear }
}
