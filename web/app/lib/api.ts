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
  providerSpecificData?: Record<string, unknown>
  createdAt?: string
  updatedAt?: string
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

export function useProviderData(providerId?: string) {
  const [providers, setProviders] = useState<Provider[]>([])
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  async function load() {
    setLoading(true)
    try {
      const suffix = providerId ? `?provider=${encodeURIComponent(providerId)}` : ""
      const response = await fetch(`/api/providers${suffix}`, { credentials: "include" })
      if (!response.ok) throw new Error(`Provider API returned ${response.status}`)
      const data = (await response.json()) as ProvidersResponse
      setProviders(data.providers ?? [])
      setConnections(data.connections ?? [])
      setError("")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load provider data")
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
    const response = await fetch(`/api/providers/${id}`, { method: "DELETE", credentials: "include" })
    if (!response.ok) throw new Error(`Delete failed with ${response.status}`)
    await load()
  }

  return { providers, connections, loading, error, reload: load, saveConnection, deleteConnection }
}

export function useModelData() {
  const [models, setModels] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  useEffect(() => {
    let cancelled = false

    async function load() {
      try {
        const response = await fetch("/api/v1/models", { credentials: "include" })
        if (!response.ok) throw new Error(`Models API returned ${response.status}`)
        const data = (await response.json()) as ModelsResponse
        if (!cancelled) {
          setModels((data.data ?? []).map((model) => model.id))
          setError("")
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Unable to load models")
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

export function useUpdateStatus() {
  const [status, setStatus] = useState<UpdateStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [updating, setUpdating] = useState(false)
  const [message, setMessage] = useState("")

  async function load() {
    setLoading(true)
    try {
      const response = await fetch("/api/update", { credentials: "include" })
      if (!response.ok) throw new Error(`Update API returned ${response.status}`)
      setStatus((await response.json()) as UpdateStatus)
    } catch (err) {
      setStatus(null)
      setMessage(err instanceof Error ? err.message : "Unable to check for updates")
    } finally {
      setLoading(false)
    }
  }

  async function update() {
    setUpdating(true)
    setMessage("")
    try {
      const response = await fetch("/api/update", { method: "POST", credentials: "include" })
      const data = (await response.json()) as { success?: boolean; error?: string; restartRequired?: boolean }
      if (!response.ok || !data.success) throw new Error(data.error || `Update failed with ${response.status}`)
      setMessage(data.restartRequired ? "Update installed. Restart BRoute to use it." : "Update installed.")
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
