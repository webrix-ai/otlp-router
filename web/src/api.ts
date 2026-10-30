export type Signal = "traces" | "metrics" | "logs"
export const SIGNALS: Signal[] = ["traces", "metrics", "logs"]

export type TargetType = "otlphttp" | "otlpgrpc" | "willow"

export type RuleType =
  | "drop_attributes"
  | "mask_attributes"
  | "hash_attributes"
  | "drop_log_events"
  | "drop_spans"
  | "drop_metrics"
  | "redact"

export interface Rule {
  type: RuleType
  keys?: string[]
  names?: string[]
  patterns?: string[]
  builtins?: boolean
  level?: "" | "resource" | "record"
  salt?: string
}

export interface Target {
  id: string
  name: string
  enabled: boolean
  type: TargetType
  endpoint?: string
  encoding?: "proto" | "json"
  compression?: "" | "gzip" | "none"
  insecure?: boolean
  headers?: Record<string, string>
  passthroughHeaders?: string[]
  signals: Record<Signal, boolean>
  agents?: string[]
  filters?: Rule[]
  willow?: WillowSettings
}

export type WillowRegion = "us" | "eu" | "custom"

export interface WillowSettings {
  org?: string
  token: string
  region?: WillowRegion
  connectUrl?: string
  baseUrl?: string
}

export const WILLOW_REGIONS: { value: WillowRegion; label: string; apex?: string }[] = [
  { value: "us", label: "US", apex: "withwillow.ai" },
  { value: "eu", label: "EU", apex: "eu.withwillow.ai" },
  { value: "custom", label: "Custom (on-prem)" }
]

export function willowEndpoint(w?: WillowSettings): string {
  if (w?.baseUrl) return w.baseUrl.replace(/\/+$/, "")
  if (w?.region === "custom") {
    const base = (w.connectUrl ?? "").trim().replace(/(\/otlp(\/v1(\/(traces|metrics|logs))?)?)?\/*$/, "")
    return `${base || "<connect-url>"}/otlp`
  }
  const region = WILLOW_REGIONS.find((r) => r.value === w?.region && r.apex) ?? WILLOW_REGIONS[0]
  return `https://${w?.org || "<org>"}.${region.apex}/otlp`
}

export interface TargetStats {
  sent: number
  failed: number
  dropped: number
  filteredBatches: number
  batches: number
  retries: number
  queueLen: number
  queueCap: number
  lastSuccessAt?: number
  lastError?: string
  lastErrorAt?: number
}

export interface AgentStats {
  agent: string
  items: Partial<Record<Signal, number>>
  lastSeenAt: number
}

export interface Stats {
  ingest: {
    items: Record<Signal, number>
    batches: Record<Signal, number>
    agents: AgentStats[] | null
  }
  targets: Record<string, TargetStats>
}

export interface Preset {
  id: string
  name: string
  description: string
  rules: Rule[]
}

export interface Info {
  version: string
  publicUrl: string
  ingestAuth: boolean
  readOnly: boolean
  store: string
  listen: { OTLPGRPC: string; OTLPHTTP: string; Admin: string }
  presets: Preset[]
}

export interface TargetsResponse {
  targets: Target[]
  version: string
}

export interface IngestToken {
  id: string
  name: string
  hint: string
  createdAt: number
  expiresAt?: number
  lastUsedAt?: number
}

export interface TokensResponse {
  tokens: IngestToken[]
  configTokens: number
  open: boolean
  rejected: number
  version: string
}

export interface TokenCreatedResponse extends TokensResponse {
  created: IngestToken
  secret: string
}

export interface PreviewResponse {
  available: boolean
  before?: unknown
  after?: unknown
  beforeItems?: number
  afterItems?: number
}

export const SECRET_MASK = "••••••••"

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body)
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 && path !== "/api/login") window.dispatchEvent(new Event("otlp-router:logout"))
    throw new ApiError(res.status, data.error ?? `Request failed (${res.status})`)
  }
  return data as T
}

export const api = {
  session: () => request<{ authenticated: boolean; username: string }>("GET", "/api/session"),
  login: (username: string, password: string) => request("POST", "/api/login", { username, password }),
  logout: () => request("POST", "/api/logout", {}),
  info: () => request<Info>("GET", "/api/info"),
  stats: () => request<Stats>("GET", "/api/stats"),
  targets: () => request<TargetsResponse>("GET", "/api/targets"),
  createTarget: (target: Target, version: string) =>
    request<TargetsResponse>("POST", "/api/targets", { target, version }),
  updateTarget: (target: Target, version: string) =>
    request<TargetsResponse>("PUT", `/api/targets/${encodeURIComponent(target.id)}`, { target, version }),
  deleteTarget: (id: string, version: string) =>
    request<TargetsResponse>("DELETE", `/api/targets/${encodeURIComponent(id)}`, { version }),
  setEnabled: (id: string, enabled: boolean, version: string) =>
    request<TargetsResponse>("POST", `/api/targets/${encodeURIComponent(id)}/enabled`, { enabled, version }),
  tokens: () => request<TokensResponse>("GET", "/api/tokens"),
  createToken: (name: string) => request<TokenCreatedResponse>("POST", "/api/tokens", { name }),
  rotateToken: (id: string, graceSeconds: number) =>
    request<TokenCreatedResponse>("POST", `/api/tokens/${encodeURIComponent(id)}/rotate`, { graceSeconds }),
  revokeToken: (id: string) => request<TokensResponse>("DELETE", `/api/tokens/${encodeURIComponent(id)}`, {}),
  test: (target: Target) => request<{ ok: boolean; error?: string }>("POST", "/api/test", { target }),
  preview: (target: Target, signal: Signal) =>
    request<PreviewResponse>("POST", "/api/preview", { target, signal })
}

export function formatNumber(n: number | undefined): string {
  if (!n) return "0"
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}k`
  return n.toLocaleString()
}

export function timeUntil(ms: number): string {
  const s = Math.max(0, Math.round((ms - Date.now()) / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86400)}d`
}

export function timeAgo(ms?: number): string {
  if (!ms) return "never"
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000))
  if (s < 5) return "just now"
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  return `${Math.round(s / 86400)}d ago`
}
