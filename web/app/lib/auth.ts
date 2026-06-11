import { useEffect, useState } from "react"

export type AuthSettings = {
  requireLogin: boolean
  hasPassword: boolean
  setupComplete: boolean
  authenticated: boolean
}

export function useAuthStatus() {
  const [settings, setSettings] = useState<AuthSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  async function load() {
    setLoading(true)
    try {
      const [settingsResponse, statusResponse] = await Promise.all([
        fetch("/api/settings/require-login", { credentials: "include" }),
        fetch("/api/auth/status", { credentials: "include" }),
      ])
      if (!settingsResponse.ok)
        throw new Error(`Settings API returned ${settingsResponse.status}`)
      if (!statusResponse.ok)
        throw new Error(`Auth status API returned ${statusResponse.status}`)
      const settingsData = await settingsResponse.json()
      const statusData = await statusResponse.json()
      setSettings({
        requireLogin: settingsData.requireLogin !== false,
        hasPassword: Boolean(settingsData.hasPassword),
        setupComplete: Boolean(settingsData.setupComplete),
        authenticated: Boolean(statusData.authenticated),
      })
      setError("")
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Unable to load auth status"
      )
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return { settings, loading, error, reload: load }
}

export async function loginWithPassword(password: string) {
  const response = await fetch("/api/auth/login", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
  })
  const data = await response.json().catch(() => ({}))
  if (!response.ok)
    throw new Error(data.error || `Login failed with ${response.status}`)
  return data
}

export async function setupPassword(password: string) {
  const response = await fetch("/api/settings/require-login", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ requireLogin: true, password }),
  })
  const data = await response.json().catch(() => ({}))
  if (!response.ok)
    throw new Error(data.error || `Setup failed with ${response.status}`)
  return data
}

export async function logout() {
  await fetch("/api/auth/logout", { method: "POST", credentials: "include" })
}
