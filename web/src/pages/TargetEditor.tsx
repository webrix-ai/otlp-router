import { useState, type ReactNode } from "react"
import { ArrowDown, ArrowUp, ChevronRight, Eye, Plug, Plus, Trash2, Wand2 } from "lucide-react"
import {
  api,
  SECRET_MASK,
  SIGNALS,
  WILLOW_REGIONS,
  willowEndpoint,
  type KnownCategory,
  type KnownRemoval,
  type PreviewResponse,
  type Rule,
  type RuleType,
  type Signal,
  type Target,
  type TargetType,
  type WillowRegion,
} from "../api"
import { KNOWN_AGENT_SLUGS, agentName } from "../agents"
import { useRouterData } from "../App"
import { JsonViewer } from "../json"
import { AgentName, ErrorBanner, Modal, Toggle } from "../ui"

const ALL_AGENTS = "*"

export function newTarget(type: TargetType): Target {
  const base: Target = {
    id: "",
    name: "",
    enabled: true,
    type,
    signals: { traces: true, metrics: true, logs: true },
    headers: {},
    filters: []
  }
  if (type === "willow") return { ...base, name: "Willow", willow: { org: "", token: "", region: "us" } }
  if (type === "otlpgrpc") return { ...base, endpoint: "" }
  return { ...base, endpoint: "", encoding: "proto", compression: "gzip" }
}

const RULES: Record<RuleType, { label: string; help: string; field: "keys" | "names" | "patterns" | "categories" }> = {
  drop_known: { label: "Drop known data", help: "Found by meaning, under each agent's own names", field: "categories" },
  hash_known: { label: "Hash known data", help: "Found by meaning, under each agent's own names", field: "categories" },
  drop_log_events: { label: "Drop log events", help: "Event names, e.g. *user_prompt", field: "names" },
  drop_attributes: { label: "Drop attributes", help: "Attribute keys, globs allowed, e.g. gen_ai.prompt*", field: "keys" },
  mask_attributes: { label: "Mask attributes", help: "Values become [REDACTED]", field: "keys" },
  hash_attributes: { label: "Hash attributes", help: "Values become a stable SHA-256, so you can still group by them", field: "keys" },
  redact: { label: "Redact text", help: "Regular expressions applied to log bodies and string attributes", field: "patterns" },
  drop_spans: { label: "Drop spans", help: "Span names", field: "names" },
  drop_metrics: { label: "Drop metrics", help: "Metric names, e.g. *.cost.usage", field: "names" }
}

// clean drops blank list entries left over from editing; an empty regex would
// match everywhere.
function clean(t: Target): Target {
  const list = (v?: string[]) => (v ?? []).map((s) => s.trim()).filter(Boolean)
  const headers = Object.fromEntries(Object.entries(t.headers ?? {}).filter(([k]) => k.trim()))
  return {
    ...t,
    headers,
    agents: list(t.agents),
    filters: (t.filters ?? []).map((r) => ({
      ...r,
      keys: r.keys && list(r.keys),
      names: r.names && list(r.names),
      patterns: r.patterns && list(r.patterns),
      categories: r.categories && list(r.categories)
    }))
  }
}

export default function TargetEditor({ initial, onClose }: { initial: Target; onClose: () => void }) {
  const { info, version, applyTargets } = useRouterData()
  const [t, setT] = useState<Target>(initial)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<{ ok: boolean; error?: string }>()
  const isNew = !initial.id
  const readOnly = info?.readOnly ?? false

  const patch = (p: Partial<Target>) => setT((prev) => ({ ...prev, ...p }))
  const setRules = (filters: Rule[]) => patch({ filters })
  const rules = t.filters ?? []

  const save = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const body = clean(t)
      applyTargets(isNew ? await api.createTarget(body, version) : await api.updateTarget(body, version))
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!confirm(`Delete target "${t.name}"? Telemetry stops flowing to it immediately.`)) return
    setBusy(true)
    try {
      applyTargets(await api.deleteTarget(t.id, version))
      onClose()
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }

  const runTest = async () => {
    setTest(undefined)
    try {
      setTest(await api.test(clean(t)))
    } catch (e) {
      setTest({ ok: false, error: (e as Error).message })
    }
  }

  return (
    <Modal
      wide
      title={isNew ? "Add target" : `Edit ${initial.name}`}
      subtitle="Where to send telemetry, and what to remove first."
      onClose={onClose}
      footer={
        <>
          {!isNew && !readOnly && (
            <button className="btn-danger mr-auto" onClick={remove} disabled={busy}>
              <Trash2 className="size-4" /> Delete
            </button>
          )}
          {test && (
            <span className={`mr-2 max-w-sm truncate text-xs ${test.ok ? "text-success" : "text-danger"}`} title={test.error}>
              {test.ok ? "Connection OK" : test.error}
            </span>
          )}
          <button className="btn-ghost" onClick={runTest}>
            <Plug className="size-4" /> Test connection
          </button>
          <button className="btn-ghost" onClick={onClose}>
            Cancel
          </button>
          {!readOnly && (
            <button className="btn-primary" onClick={save} disabled={busy}>
              {busy ? "Saving…" : isNew ? "Add target" : "Save changes"}
            </button>
          )}
        </>
      }
    >
      <ErrorBanner message={error} />
      <div className="grid gap-8 lg:grid-cols-[1fr_1fr]">
        <div className="space-y-8">
          {isNew && (
            <Section title="Type">
              <div className="grid grid-cols-3 gap-2">
                {(
                  [
                    ["willow", "Willow", "Governance & analytics"],
                    ["otlphttp", "OTLP / HTTP", "Datadog, Grafana, Honeycomb…"],
                    ["otlpgrpc", "OTLP / gRPC", "Collectors, Tempo, Jaeger…"]
                  ] as const
                ).map(([type, label, hint]) => (
                  <button
                    key={type}
                    onClick={() => setT({ ...newTarget(type), name: t.name && t.type === type ? t.name : newTarget(type).name })}
                    className={`rounded-xl border p-3 text-left transition ${
                      t.type === type ? "border-primary/60 bg-primary/10" : "border-line hover:border-line-strong"
                    }`}
                  >
                    <div className="flex items-center gap-1.5 text-sm font-medium">
                      {type === "willow" && <img src="/willow-logo-green.svg" alt="" className="size-4 rounded-full bg-logo-bg p-0.5" />}
                      {label}
                    </div>
                    <div className="mt-0.5 text-[11px] text-faint">{hint}</div>
                  </button>
                ))}
              </div>
            </Section>
          )}

          <Section title="Connection">
            <Field label="Name">
              <input className="input" value={t.name} placeholder="Datadog" onChange={(e) => patch({ name: e.target.value })} />
            </Field>
            {t.type === "willow" ? (
              <>
                <div className="grid grid-cols-[1fr_11rem] gap-3">
                  {t.willow?.region === "custom" ? (
                    <Field label="Connect URL">
                      <input
                        className="input font-mono"
                        value={t.willow?.connectUrl ?? ""}
                        placeholder="https://willow.acme.internal"
                        onChange={(e) => patch({ willow: { ...t.willow!, connectUrl: e.target.value } })}
                      />
                    </Field>
                  ) : (
                    <Field label="Willow org">
                      <input
                        className="input"
                        value={t.willow?.org ?? ""}
                        placeholder="acme"
                        onChange={(e) => patch({ willow: { ...t.willow!, org: e.target.value } })}
                      />
                    </Field>
                  )}
                  <Field label="Region">
                    <select
                      className="input"
                      value={t.willow?.region || "us"}
                      onChange={(e) => patch({ willow: { ...t.willow!, region: e.target.value as WillowRegion } })}
                    >
                      {WILLOW_REGIONS.map((r) => (
                        <option key={r.value} value={r.value}>
                          {r.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                </div>
                <Field label="OTLP token">
                  <input
                    className="input font-mono"
                    value={t.willow?.token ?? ""}
                    placeholder="${env:WILLOW_TOKEN}"
                    onFocus={(e) => e.target.value === SECRET_MASK && e.target.select()}
                    onChange={(e) => patch({ willow: { ...t.willow!, token: e.target.value } })}
                  />
                </Field>
                <p className="text-xs text-faint">
                  Sends to{" "}
                  <span className="font-mono text-muted">
                    {willowEndpoint(t.willow)}
                  </span>{" "}
                  as OTLP/JSON, authenticated with your org's Willow OTLP token.
                  {t.willow?.region === "custom" && " Use the URL your users open Willow's portal at, on-prem or on a custom domain."}
                </p>
              </>
            ) : (
              <>
                <Field label={t.type === "otlpgrpc" ? "Endpoint (host:port)" : "Base URL"}>
                  <input
                    className="input font-mono"
                    value={t.endpoint ?? ""}
                    placeholder={t.type === "otlpgrpc" ? "otel-collector.observability:4317" : "https://otlp.example.com"}
                    onChange={(e) => patch({ endpoint: e.target.value })}
                  />
                </Field>
                <div className="grid grid-cols-3 gap-3">
                  {t.type === "otlphttp" && (
                    <Field label="Encoding">
                      <select className="input" value={t.encoding} onChange={(e) => patch({ encoding: e.target.value as "proto" | "json" })}>
                        <option value="proto">Protobuf</option>
                        <option value="json">JSON</option>
                      </select>
                    </Field>
                  )}
                  <Field label="Compression">
                    <select className="input" value={t.compression || "none"} onChange={(e) => patch({ compression: e.target.value as "gzip" | "none" })}>
                      <option value="gzip">gzip</option>
                      <option value="none">none</option>
                    </select>
                  </Field>
                  <Field label="Skip TLS verify">
                    <div className="pt-1.5">
                      <Toggle checked={!!t.insecure} onChange={(insecure) => patch({ insecure })} label="Skip TLS verification" />
                    </div>
                  </Field>
                </div>
              </>
            )}
          </Section>

          <Section title="Headers" hint="Use ${env:NAME} to read a secret from the router's environment instead of storing it.">
            <HeadersEditor headers={t.headers ?? {}} onChange={(headers) => patch({ headers })} />
          </Section>

          <Section title="What to send">
            <div className="flex gap-6">
              {SIGNALS.map((s) => (
                <label key={s} className="flex items-center gap-2 text-sm capitalize">
                  <Toggle checked={t.signals[s]} onChange={(v) => patch({ signals: { ...t.signals, [s]: v } })} label={s} />
                  {s}
                </label>
              ))}
            </div>
            <div>
              <div className="label mt-4">Agents</div>
              <div className="flex flex-wrap gap-1.5">
                {[ALL_AGENTS, ...KNOWN_AGENT_SLUGS].map((a) => {
                  const all = a === ALL_AGENTS
                  const selected = t.agents ?? []
                  const on = all ? selected.length === 0 : selected.includes(a)
                  const next = all ? [] : on ? selected.filter((x) => x !== a) : [...selected, a]
                  return (
                    <button
                      key={a}
                      onClick={() => patch({ agents: next.length ? next : undefined })}
                      className={`rounded-full border px-2.5 py-1 text-xs transition ${
                        on ? "border-primary/60 bg-primary/15 text-primary" : "border-line text-muted hover:border-line-strong"
                      }`}
                    >
                      {all ? "All agents" : <AgentName slug={a} />}
                    </button>
                  )
                })}
              </div>
              <p className="mt-2 text-xs text-faint">
                {(t.agents ?? []).length === 0
                  ? "Every agent is sent, including ones not listed here. Pick some to send only those."
                  : "Only the selected agents are sent."}
              </p>
            </div>
          </Section>
        </div>

        <div className="space-y-8">
          <Section title="Filters" hint="Applied to this target's copy only. Other targets are unaffected.">
            <FilterControls rules={rules} onChange={setRules} />
          </Section>

          <Preview target={t} />
        </div>
      </div>
    </Modal>
  )
}

function emptyRule(type: RuleType): Rule {
  const r: Rule = { type }
  r[RULES[type].field] = type === "drop_known" ? ["prompts"] : [""]
  if (type === "redact") r.builtins = true
  return r
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <div>
        <h3 className="text-sm font-semibold">{title}</h3>
        {hint && <p className="mt-0.5 text-xs text-faint">{hint}</p>}
      </div>
      {children}
    </section>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="label">{label}</div>
      {children}
    </div>
  )
}

function HeadersEditor({ headers, onChange }: { headers: Record<string, string>; onChange: (h: Record<string, string>) => void }) {
  const rows = Object.entries(headers)
  const set = (i: number, k: string, v: string) => onChange(Object.fromEntries(rows.map((r, j) => (j === i ? [k, v] : r))))
  return (
    <div className="space-y-2">
      {rows.map(([k, v], i) => (
        <div key={i} className="flex gap-2">
          <input className="input w-2/5 font-mono" placeholder="DD-API-KEY" value={k} onChange={(e) => set(i, e.target.value, v)} />
          <input
            className="input flex-1 font-mono"
            placeholder="${env:DD_API_KEY}"
            value={v}
            onFocus={(e) => e.target.value === SECRET_MASK && e.target.select()}
            onChange={(e) => set(i, k, e.target.value)}
          />
          <button className="btn-ghost px-2.5" aria-label="Remove header" onClick={() => onChange(Object.fromEntries(rows.filter((_, j) => j !== i)))}>
            <Trash2 className="size-4" />
          </button>
        </div>
      ))}
      <button className="btn-ghost py-1.5 text-xs" onClick={() => onChange({ ...headers, "": "" })} disabled={"" in headers}>
        <Plus className="size-3.5" /> Add header
      </button>
    </div>
  )
}

function RuleEditor({
  rule,
  onChange,
  onRemove,
  onMove
}: {
  rule: Rule
  onChange: (r: Rule) => void
  onRemove: () => void
  onMove: (d: -1 | 1) => void
}) {
  const meta = RULES[rule.type]
  const values = rule[meta.field] ?? []
  return (
    <div className="rounded-xl border border-line bg-well p-3">
      <div className="mb-2 flex items-center gap-2">
        <Wand2 className="size-3.5 text-primary" />
        <span className="text-sm font-medium">{meta.label}</span>
        <span className="ml-auto flex gap-0.5 text-faint">
          <IconBtn label="Move up" onClick={() => onMove(-1)}>
            <ArrowUp className="size-3.5" />
          </IconBtn>
          <IconBtn label="Move down" onClick={() => onMove(1)}>
            <ArrowDown className="size-3.5" />
          </IconBtn>
          <IconBtn label="Remove rule" onClick={onRemove}>
            <Trash2 className="size-3.5" />
          </IconBtn>
        </span>
      </div>
      {meta.field === "categories" ? (
        <KnownDataPicker selected={values} onChange={(categories) => onChange({ ...rule, categories })} />
      ) : (
        <textarea
          className="input min-h-16 font-mono text-xs"
          rows={Math.min(6, Math.max(2, values.length))}
          placeholder={meta.help}
          value={values.join("\n")}
          onChange={(e) => onChange({ ...rule, [meta.field]: e.target.value.split("\n") })}
        />
      )}
      <div className="mt-2 flex flex-wrap items-center gap-4 text-xs text-muted">
        <span className="text-faint">{meta.field === "categories" ? `${meta.help}.` : `One per line. ${meta.help}.`}</span>
        {rule.type === "redact" && (
          <label className="flex items-center gap-2">
            <Toggle checked={!!rule.builtins} onChange={(builtins) => onChange({ ...rule, builtins })} label="Built-in secret patterns" />
            Built-in secret patterns
          </label>
        )}
        {meta.field === "keys" && (
          <select className="input w-auto py-1 text-xs" value={rule.level ?? ""} onChange={(e) => onChange({ ...rule, level: e.target.value as Rule["level"] })}>
            <option value="">Resource and record</option>
            <option value="resource">Resource only</option>
            <option value="record">Record only</option>
          </select>
        )}
        {rule.type === "hash_attributes" && (
          <input className="input w-48 py-1 font-mono text-xs" placeholder="salt, e.g. ${env:HASH_SALT}" value={rule.salt ?? ""} onChange={(e) => onChange({ ...rule, salt: e.target.value })} />
        )}
      </div>
    </div>
  )
}

type IdentityMode = "keep" | "hash" | "drop"

// Filters are one rule list. The choices own the first drop_known, hash_known
// (identity) and redact rules; every other rule is a custom rule.
interface FilterState {
  categories: string[]
  identity: IdentityMode
  salt: string
  secrets: string[]
  patterns: string[]
  custom: Rule[]
}

const CUSTOM_RULES: RuleType[] = ["drop_log_events", "drop_attributes", "mask_attributes", "hash_attributes", "drop_spans", "drop_metrics"]

function splitRules(rules: Rule[], allSecrets: string[]): FilterState {
  const s: FilterState = { categories: [], identity: "keep", salt: "", secrets: [], patterns: [], custom: [] }
  let known = false
  let hashed = false
  let redact = false
  for (const r of rules) {
    if (r.type === "drop_known" && !known) {
      known = true
      const categories = r.categories ?? []
      s.categories = categories.filter((c) => c !== "identity")
      if (categories.includes("identity")) s.identity = "drop"
    } else if (r.type === "hash_known" && !hashed && (r.categories ?? []).join() === "identity") {
      hashed = true
      s.identity = "hash"
      s.salt = r.salt ?? ""
    } else if (r.type === "redact" && !redact) {
      redact = true
      s.secrets = r.builtins ? allSecrets : (r.secrets ?? [])
      s.patterns = r.patterns ?? []
    } else {
      s.custom.push(r)
    }
  }
  return s
}

function joinRules(s: FilterState): Rule[] {
  const out: Rule[] = []
  const categories = [...s.categories, ...(s.identity === "drop" ? ["identity"] : [])]
  if (categories.length) out.push({ type: "drop_known", categories })
  if (s.identity === "hash") out.push({ type: "hash_known", categories: ["identity"], salt: s.salt || undefined })
  if (s.secrets.length || s.patterns.some((p) => p.trim())) out.push({ type: "redact", secrets: s.secrets, patterns: s.patterns })
  return [...out, ...s.custom]
}

function FilterControls({ rules, onChange }: { rules: Rule[]; onChange: (r: Rule[]) => void }) {
  const { info } = useRouterData()
  const allSecrets = (info?.secretPatterns ?? []).map((p) => p.id)
  const s = splitRules(rules, allSecrets)
  const set = (p: Partial<FilterState>) => onChange(joinRules({ ...s, ...p }))
  const setCustom = (custom: Rule[]) => set({ custom })
  const identity = info?.knownData.find((c) => c.id === "identity")

  return (
    <div className="space-y-6">
      <FilterGroup title="Agent data" hint="Send or remove each kind of data. The router finds it under each agent's own names.">
        <KnownDataPicker selected={s.categories} onChange={(categories) => set({ categories })} exclude={["identity"]} />
      </FilterGroup>

      {identity && (
        <FilterGroup title="User identity" hint={identity.description}>
          <IdentityControl
            category={identity}
            mode={s.identity}
            salt={s.salt}
            onChange={(identity, salt) => set({ identity, salt })}
          />
        </FilterGroup>
      )}

      <FilterGroup title="Secrets" hint="Replaced with [REDACTED] in log bodies and string attributes.">
        <SecretsControl secrets={s.secrets} patterns={s.patterns} onChange={(secrets, patterns) => set({ secrets, patterns })} />
      </FilterGroup>

      <FilterGroup title="Custom rules" hint="Run after the choices above, in order.">
        <div className="space-y-2">
          {s.custom.map((r, i) => (
            <RuleEditor
              key={i}
              rule={r}
              onChange={(next) => setCustom(s.custom.map((x, j) => (j === i ? next : x)))}
              onRemove={() => setCustom(s.custom.filter((_, j) => j !== i))}
              onMove={(d) => {
                const next = [...s.custom]
                const j = i + d
                if (j < 0 || j >= next.length) return
                ;[next[i], next[j]] = [next[j], next[i]]
                setCustom(next)
              }}
            />
          ))}
        </div>
        <AddRule types={CUSTOM_RULES} onAdd={(type) => setCustom([...s.custom, emptyRule(type)])} />
      </FilterGroup>

      <FilterGroup title="Everything this target removes" hint="Read only. Every choice and custom rule above, as what happens to the data.">
        <EffectiveFilters rules={rules} />
      </FilterGroup>
    </div>
  )
}

const EFFECTS = [
  "Log events dropped",
  "Log bodies emptied",
  "Records dropped",
  "Spans dropped",
  "Metrics dropped",
  "Attributes dropped",
  "Attributes masked",
  "Attributes hashed",
  "Text redacted"
] as const

type Effect = (typeof EFFECTS)[number]

interface EffectItem {
  value: string
  detail?: string
  from: string[]
}

const LEVELS: Record<string, string> = { resource: "resource only", record: "record only" }

function effectsOf(rules: Rule[], known: KnownCategory[], secrets: { id: string; name: string; pattern: string }[]) {
  const out = new Map<Effect, Map<string, EffectItem>>()
  const add = (effect: Effect, values: string[] | undefined, from: string, detail?: (v: string) => string | undefined) => {
    for (const raw of values ?? []) {
      const value = raw.trim()
      if (!value) continue
      const items = out.get(effect) ?? new Map<string, EffectItem>()
      out.set(effect, items)
      const item = items.get(value) ?? { value, detail: detail?.(value), from: [] }
      if (!item.from.includes(from)) item.from.push(from)
      items.set(value, item)
    }
  }
  const sourceOf = (r: KnownRemoval) =>
    r.source === "agent" ? (r.agents ?? []).map(agentName).join(", ") : r.source === "standard" ? "OpenTelemetry standard" : "unknown agents"

  for (const r of rules) {
    switch (r.type) {
      case "drop_known":
      case "hash_known":
        for (const c of known.filter((k) => r.categories?.includes(k.id))) {
          for (const rm of c.removes) {
            const from = `${c.name} · ${sourceOf(rm)}`
            if (r.type === "hash_known") {
              add("Attributes hashed", rm.attributes, from)
              continue
            }
            add("Log events dropped", rm.events, from)
            add("Log bodies emptied", rm.bodies, from)
            add("Records dropped", rm.rules, from)
            add("Metrics dropped", rm.metrics, from)
            add("Attributes dropped", rm.attributes, from)
          }
        }
        break
      case "drop_log_events":
        add("Log events dropped", r.names, "Custom rule")
        break
      case "drop_spans":
        add("Spans dropped", r.names, "Custom rule")
        break
      case "drop_metrics":
        add("Metrics dropped", r.names, "Custom rule")
        break
      case "drop_attributes":
      case "mask_attributes":
      case "hash_attributes": {
        const effect: Effect =
          r.type === "drop_attributes" ? "Attributes dropped" : r.type === "mask_attributes" ? "Attributes masked" : "Attributes hashed"
        add(effect, r.keys, r.level ? `Custom rule · ${LEVELS[r.level]}` : "Custom rule")
        break
      }
      case "redact": {
        const ids = r.builtins ? secrets.map((p) => p.id) : (r.secrets ?? [])
        for (const p of secrets.filter((p) => ids.includes(p.id))) add("Text redacted", [p.name], "Secrets", () => p.pattern)
        add("Text redacted", r.patterns, "Custom pattern")
        break
      }
    }
  }
  return EFFECTS.flatMap((effect) => {
    const items = out.get(effect)
    return items ? [{ effect, items: [...items.values()] }] : []
  })
}

function EffectiveFilters({ rules }: { rules: Rule[] }) {
  const { info } = useRouterData()
  const [raw, setRaw] = useState(false)
  const groups = effectsOf(rules, info?.knownData ?? [], info?.secretPatterns ?? [])
  return (
    <div className="space-y-2">
      <div className="space-y-3 rounded-lg border border-line bg-surface/60 p-3">
        {groups.length === 0 && <p className="text-xs text-faint">Nothing is removed. Telemetry is sent as received.</p>}
        {groups.map(({ effect, items }) => (
          <div key={effect} className="space-y-1">
            <div className="text-[11px] font-medium text-fg">
              {effect} <span className="font-normal text-faint">({items.length})</span>
            </div>
            {effect === "Text redacted" ? (
              <div className="space-y-0.5">
                {items.map((i) => (
                  <div key={i.value} className="flex items-baseline gap-2 text-[11px]" title={i.from.join("\n")}>
                    <span className="w-40 shrink-0 truncate text-muted">{i.detail ? i.value : "Custom pattern"}</span>
                    <code className="truncate font-mono text-faint">{i.detail ?? i.value}</code>
                  </div>
                ))}
              </div>
            ) : (
              <div className="flex flex-wrap gap-1">
                {items.map((i) => (
                  <span key={i.value} title={i.from.join("\n")} className="rounded bg-line/60 px-1.5 py-0.5 font-mono text-[11px] text-muted">
                    {i.value}
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
      <p className="text-[11px] text-faint">Hover an item to see which choice or rule adds it.</p>
      <ShowRemovals expanded={raw} onToggle={() => setRaw(!raw)} label="Show saved rules" />
      {raw && <JsonViewer title="filters" value={JSON.parse(JSON.stringify(clean({ filters: rules } as Target).filters))} />}
    </div>
  )
}

function FilterGroup({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <div>
        <div className="label mb-0">{title}</div>
        {hint && <p className="text-xs text-faint">{hint}</p>}
      </div>
      {children}
    </div>
  )
}

function ShowRemovals({ expanded, onToggle, label = "What it removes" }: { expanded: boolean; onToggle: () => void; label?: string }) {
  return (
    <button className="mt-1 inline-flex items-center gap-1 text-[11px] text-primary hover:underline" aria-expanded={expanded} onClick={onToggle}>
      <ChevronRight className={`size-3 transition ${expanded ? "rotate-90" : ""}`} />
      {label}
    </button>
  )
}

function KnownDataPicker({
  selected,
  onChange,
  exclude = []
}: {
  selected: string[]
  onChange: (c: string[]) => void
  exclude?: string[]
}) {
  const { info } = useRouterData()
  const [open, setOpen] = useState<string>()
  return (
    <div className="space-y-1.5">
      {(info?.knownData ?? [])
        .filter((c) => !exclude.includes(c.id))
        .map((c) => {
          const on = selected.includes(c.id)
          const covers = [...new Set(c.agents.map(agentName)), ...(c.standard ? ["OpenTelemetry standard names"] : [])]
          const expanded = open === c.id
          return (
            <div key={c.id} className="rounded-lg px-1 py-1">
              <div className="flex items-start gap-3">
                <span className="min-w-0 flex-1">
                  <span className="flex items-center justify-between gap-3">
                    <span className="text-sm">{c.name}</span>
                    <Choice
                      label={c.name}
                      value={on ? "remove" : "send"}
                      options={SEND_OR_REMOVE}
                      onChange={(v) => onChange(v === "remove" ? [...selected, c.id] : selected.filter((x) => x !== c.id))}
                    />
                  </span>
                  <span className="block text-xs text-faint">{c.description}</span>
                  <span className="block text-[11px] text-muted">{covers.join(", ")}</span>
                  <ShowRemovals expanded={expanded} onToggle={() => setOpen(expanded ? undefined : c.id)} />
                </span>
              </div>
              {expanded && <KnownRemovals removals={c.removes} />}
            </div>
          )
        })}
    </div>
  )
}

interface ChoiceOption<T extends string> {
  value: T
  label: string
  removes?: boolean
}

function Choice<T extends string>({
  label,
  value,
  options,
  onChange
}: {
  label: string
  value: T
  options: ChoiceOption<T>[]
  onChange: (v: T) => void
}) {
  return (
    <div className="inline-flex shrink-0 rounded-lg border border-line p-0.5" role="radiogroup" aria-label={label}>
      {options.map((o) => {
        const on = value === o.value
        return (
          <button
            key={o.value}
            role="radio"
            aria-checked={on}
            onClick={() => onChange(o.value)}
            className={`rounded-md px-2.5 py-0.5 text-xs transition ${
              on ? (o.removes ? "bg-danger/15 font-medium text-danger" : "bg-primary/15 font-medium text-primary") : "text-muted hover:text-fg"
            }`}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

const SEND_OR_REMOVE: ChoiceOption<"send" | "remove">[] = [
  { value: "send", label: "Send" },
  { value: "remove", label: "Remove", removes: true }
]

const SEND_OR_REDACT: ChoiceOption<"send" | "redact">[] = [
  { value: "send", label: "Send" },
  { value: "redact", label: "Redact", removes: true }
]

const IDENTITY_MODES: ChoiceOption<IdentityMode>[] = [
  { value: "keep", label: "Send" },
  { value: "hash", label: "Hash", removes: true },
  { value: "drop", label: "Remove", removes: true }
]

function IdentityControl({
  category,
  mode,
  salt,
  onChange
}: {
  category: KnownCategory
  mode: IdentityMode
  salt: string
  onChange: (mode: IdentityMode, salt: string) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="px-1">
      <div className="flex flex-wrap items-center gap-3">
        <Choice label="User identity" value={mode} options={IDENTITY_MODES} onChange={(m) => onChange(m, salt)} />
        {mode === "hash" && (
          <input
            className="input w-56 py-1 font-mono text-xs"
            placeholder="salt, e.g. ${env:HASH_SALT}"
            value={salt}
            onChange={(e) => onChange(mode, e.target.value)}
          />
        )}
      </div>
      <ShowRemovals expanded={open} onToggle={() => setOpen(!open)} label="What it covers" />
      {open && <KnownRemovals removals={category.removes} />}
    </div>
  )
}

function SecretsControl({
  secrets,
  patterns,
  onChange
}: {
  secrets: string[]
  patterns: string[]
  onChange: (secrets: string[], patterns: string[]) => void
}) {
  const { info } = useRouterData()
  const all = info?.secretPatterns ?? []
  return (
    <div className="space-y-1.5">
      <div className="flex gap-3 px-1 text-[11px]">
        <button className="text-primary hover:underline" onClick={() => onChange(all.map((p) => p.id), patterns)}>
          Redact all
        </button>
        <button className="text-primary hover:underline" onClick={() => onChange([], patterns)}>
          Send all
        </button>
      </div>
      {all.map((p) => (
        <div key={p.id} className="flex items-center gap-3 px-1 py-0.5">
          <span className="min-w-0 flex-1">
            <span className="text-sm">{p.name}</span>
            <code className="block truncate font-mono text-[11px] text-faint" title={p.pattern}>
              {p.pattern}
            </code>
          </span>
          <Choice
            label={p.name}
            value={secrets.includes(p.id) ? "redact" : "send"}
            options={SEND_OR_REDACT}
            onChange={(v) => onChange(v === "redact" ? [...secrets, p.id] : secrets.filter((x) => x !== p.id), patterns)}
          />
        </div>
      ))}
      <div className="px-1 pt-2">
        <div className="text-xs text-muted">Also redact these patterns</div>
        <textarea
          className="input mt-1 min-h-14 font-mono text-xs"
          rows={Math.min(6, Math.max(2, patterns.length))}
          placeholder="One regular expression per line, e.g. ACME-[0-9]{8}"
          value={patterns.join("\n")}
          onChange={(e) => onChange(secrets, e.target.value.split("\n"))}
        />
      </div>
    </div>
  )
}

const REMOVAL_SOURCE: Record<KnownRemoval["source"], string> = {
  standard: "Any agent, by OpenTelemetry standard names",
  agent: "",
  other: "Agents the router has no profile for"
}

function KnownRemovals({ removals }: { removals: KnownRemoval[] }) {
  return (
    <div className="mt-2 space-y-2.5 rounded-lg border border-line bg-surface/60 p-2.5">
      {removals.map((r, i) => (
        <div key={i} className="space-y-1">
          <div className="text-[11px] font-medium text-fg">
            {r.source === "agent" ? (r.agents ?? []).map(agentName).join(", ") : REMOVAL_SOURCE[r.source]}
          </div>
          <RemovalList label="Events dropped" values={r.events} />
          <RemovalList label="Attributes" values={r.attributes} />
          <RemovalList label="Metrics dropped" values={r.metrics} />
          <RemovalList label="Records dropped" values={r.rules} plain />
        </div>
      ))}
    </div>
  )
}

function RemovalList({ label, values, plain }: { label: string; values?: string[]; plain?: boolean }) {
  if (!values?.length) return null
  return (
    <div className="flex flex-wrap items-baseline gap-1">
      <span className="w-28 shrink-0 text-[11px] text-faint">{label}</span>
      {values.map((v) => (
        <span key={v} className={`rounded bg-line/60 px-1.5 py-0.5 text-[11px] text-muted ${plain ? "" : "font-mono"}`}>
          {v}
        </span>
      ))}
    </div>
  )
}

function IconBtn({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button aria-label={label} title={label} onClick={onClick} className="rounded p-1 hover:bg-line hover:text-fg">
      {children}
    </button>
  )
}

function AddRule({ types, onAdd }: { types: RuleType[]; onAdd: (t: RuleType) => void }) {
  return (
    <select
      className="input w-auto text-sm"
      value=""
      onChange={(e) => {
        if (e.target.value) onAdd(e.target.value as RuleType)
      }}
    >
      <option value="">+ Add custom rule…</option>
      {types.map((k) => (
        <option key={k} value={k}>
          {RULES[k].label}
        </option>
      ))}
    </select>
  )
}

function Preview({ target }: { target: Target }) {
  const [signal, setSignal] = useState<Signal>("logs")
  const [result, setResult] = useState<PreviewResponse>()
  const [error, setError] = useState<string>()

  const run = async () => {
    setError(undefined)
    try {
      setResult(await api.preview(clean(target), signal))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <Section title="Preview" hint="Runs this target's filters on the latest payload the router received. Nothing is sent.">
      <div className="flex items-center gap-2">
        <select className="input w-auto" value={signal} onChange={(e) => setSignal(e.target.value as Signal)}>
          {SIGNALS.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <button className="btn-ghost" onClick={run}>
          <Eye className="size-4" /> Preview
        </button>
        {result?.available && (
          <span className="text-xs text-muted">
            {result.beforeItems} → <span className="text-primary">{result.afterItems}</span> items
          </span>
        )}
      </div>
      <ErrorBanner message={error} />
      {result && !result.available && <p className="text-xs text-faint">No {signal} received yet. Send some telemetry first.</p>}
      {result?.available && (
        <div className="grid gap-2">
          <JsonViewer title="Received" value={result.before} />
          <JsonViewer title="Sent to this target" value={result.after} highlight />
        </div>
      )}
    </Section>
  )
}
