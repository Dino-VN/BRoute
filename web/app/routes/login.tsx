import type React from "react"
import { useEffect, useState } from "react"
import { LockKeyhole } from "lucide-react"
import { useNavigate, useSearchParams } from "react-router"

import { Button } from "~/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card"
import { Input } from "~/components/ui/input"
import { FormField, Brand } from "~/components/console-layout"
import { loginWithPassword, setupPassword, useAuthStatus } from "~/lib/auth"

export default function Login() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const redirect = searchParams.get("redirect") || "/"
  const { settings, loading, error, reload } = useAuthStatus()
  const [password, setPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState("")

  useEffect(() => {
    if (settings && (!settings.requireLogin || settings.authenticated)) {
      navigate(redirect, { replace: true })
    }
  }, [settings, redirect, navigate])

  const setupMode = settings && !settings.hasPassword

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setFormError("")
    if (!password) {
      setFormError("Password is required.")
      return
    }
    if (setupMode && password !== confirmPassword) {
      setFormError("Password confirmation does not match.")
      return
    }

    setSubmitting(true)
    try {
      if (setupMode) {
        await setupPassword(password)
        await loginWithPassword(password)
      } else {
        await loginWithPassword(password)
      }
      await reload()
      navigate(redirect, { replace: true })
    } catch (err) {
      setFormError(err instanceof Error ? err.message : "Authentication failed")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="grid min-h-svh place-items-center bg-muted/30 p-6">
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-4">
          <Brand />
          <div className="grid gap-1">
            <div className="flex items-center gap-2">
              <LockKeyhole className="size-5 text-muted-foreground" />
              <CardTitle>
                {setupMode ? "Set admin password" : "Login"}
              </CardTitle>
            </div>
            <CardDescription>
              {setupMode
                ? "Create the password required to manage OmniRoute."
                : "Enter the admin password to continue."}
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="text-sm text-muted-foreground">
              Checking authentication...
            </p>
          ) : (
            <form onSubmit={submit} className="space-y-4">
              {error && (
                <p className="rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive">
                  {error}
                </p>
              )}
              {formError && (
                <p className="rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive">
                  {formError}
                </p>
              )}
              <FormField label="Password">
                <Input
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoFocus
                />
              </FormField>
              {setupMode && (
                <FormField label="Confirm password">
                  <Input
                    type="password"
                    value={confirmPassword}
                    onChange={(event) => setConfirmPassword(event.target.value)}
                  />
                </FormField>
              )}
              <Button type="submit" className="w-full" disabled={submitting}>
                {submitting
                  ? "Please wait..."
                  : setupMode
                    ? "Create password"
                    : "Login"}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </main>
  )
}
