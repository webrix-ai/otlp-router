import { useState, type FormEvent } from "react"
import { ArrowRight, Lock } from "lucide-react"
import { api } from "../api"
import { Brand, ErrorBanner, WILLOW_URL } from "../ui"

export default function Login({ onLogin }: { onLogin: (username: string) => void }) {
  const [username, setUsername] = useState("admin")
  const [password, setPassword] = useState("")
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      await api.login(username, password)
      onLogin(username)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid min-h-full lg:grid-cols-[1.1fr_1fr]">
      <div className="relative hidden overflow-hidden border-r border-line bg-ink/50 p-12 lg:flex lg:flex-col">
        <Brand />
        <div className="my-auto max-w-md">
          <p className="mb-4 text-xs font-medium tracking-[0.2em] text-lime uppercase">Open source OTLP router</p>
          <h1 className="text-4xl leading-tight font-semibold tracking-tight">
            One endpoint for your AI agents.
            <span className="text-muted"> Every destination, filtered your way.</span>
          </h1>
          <p className="mt-6 text-sm leading-relaxed text-muted">
            Point Claude Code, Codex, Gemini CLI, Copilot and your own agents at one ingress. Fan their telemetry out to
            Datadog, Grafana, your SIEM and Willow, and decide per destination which prompts, identities and secrets
            leave your network.
          </p>
          <div className="mt-10 grid grid-cols-3 gap-3 text-center text-xs">
            {["gRPC + HTTP", "Per-target filters", "Helm in one line"].map((t) => (
              <div key={t} className="rounded-xl border border-line bg-surface/60 px-3 py-3 text-muted">
                {t}
              </div>
            ))}
          </div>
        </div>
        <a href={WILLOW_URL} target="_blank" rel="noreferrer" className="flex items-center gap-2 text-xs text-faint hover:text-muted">
          Built and maintained by
          <img src="/willow-full-logo-white.svg" alt="Willow" className="h-3.5 opacity-80" />
        </a>
        <div className="pointer-events-none absolute -right-40 -bottom-40 size-[520px] rounded-full bg-lime/10 blur-3xl" />
      </div>

      <div className="flex items-center justify-center p-8">
        <form onSubmit={submit} className="card w-full max-w-sm p-8">
          <div className="mb-8 lg:hidden">
            <Brand />
          </div>
          <div className="mb-6 flex items-center gap-2 text-muted">
            <Lock className="size-4" />
            <span className="text-sm">Sign in to manage routing</span>
          </div>
          <ErrorBanner message={error} />
          <label className="label" htmlFor="username">
            Username
          </label>
          <input id="username" className="input mb-4" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
          <label className="label" htmlFor="password">
            Password
          </label>
          <input
            id="password"
            type="password"
            className="input mb-6"
            autoComplete="current-password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <button className="btn-primary w-full" disabled={busy || !password}>
            {busy ? "Signing in…" : "Sign in"} <ArrowRight className="size-4" />
          </button>
          <p className="mt-6 text-center text-xs text-faint">The admin login is set in the router's config.</p>
        </form>
      </div>
    </div>
  )
}
