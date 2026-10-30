import { useState, type ReactNode } from "react"
import { ArrowDown, ArrowUp, Eye, Plug, Plus, Sparkles, Trash2, Wand2 } from "lucide-react"
import { api, SECRET_MASK, SIGNALS, type PreviewResponse, type Rule, type RuleType, type Signal, type Target, type TargetType } from "../api"
import { KNOWN_AGENT_SLUGS, agentName } from "../agents"
import { useRouterData } from "../App"
import { ErrorBanner, Modal, Toggle } from "../ui"

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
  if (type === "willow") return { ...base, name: "Willow", willow: { org: "", token: "" } }
  if (type === "otlpgrpc") return { ...base, endpoint: "" }
  return { ...base, endpoint: "", encoding: "proto", compression: "gzip" }
}

const RULES: Record<RuleType, { label: string; help: string; field: "keys" | "names" | "patterns" }> = {
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
      patterns: r.patterns && list(r.patterns)
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
            <span className={`mr-2 max-w-sm truncate text-xs ${test.ok ? "text-mint" : "text-danger"}`} title={test.error}>
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
                      t.type === type ? "border-lime/60 bg-lime/10" : "border-line hover:border-line-strong"
                    }`}
                  >
                    <div className="flex items-center gap-1.5 text-sm font-medium">
                      {type === "willow" && <img src="/willow-logo-green.svg" alt="" className="size-3.5" />}
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
                <div className="grid grid-cols-2 gap-3">
                  <Field label="Willow org">
                    <input
                      className="input"
                      value={t.willow?.org ?? ""}
                      placeholder="acme"
                      onChange={(e) => patch({ willow: { ...t.willow!, org: e.target.value } })}
                    />
                  </Field>
                  <Field label="OTLP token">
                    <input
                      className="input font-mono"
                      value={t.willow?.token ?? ""}
                      placeholder="${env:WILLOW_TOKEN}"
                      onFocus={(e) => e.target.value === SECRET_MASK && e.target.select()}
                      onChange={(e) => patch({ willow: { ...t.willow!, token: e.target.value } })}
                    />
                  </Field>
                </div>
                <p className="text-xs text-faint">
                  Sends to{" "}
                  <span className="font-mono text-muted">
                    {t.willow?.baseUrl || `https://${t.willow?.org || "<org>"}.withwillow.ai/otlp`}
                  </span>{" "}
                  as OTLP/JSON, authenticated with your org's Willow OTLP token.
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
                {KNOWN_AGENT_SLUGS.map((a) => {
                  const on = (t.agents ?? []).includes(a)
                  return (
                    <button
                      key={a}
                      onClick={() => patch({ agents: on ? t.agents!.filter((x) => x !== a) : [...(t.agents ?? []), a] })}
                      className={`rounded-full border px-2.5 py-1 text-xs transition ${
                        on ? "border-lime/60 bg-lime/15 text-lime" : "border-line text-muted hover:border-line-strong"
                      }`}
                    >
                      {agentName(a)}
                    </button>
                  )
                })}
              </div>
              <p className="mt-2 text-xs text-faint">
                {(t.agents ?? []).length === 0 ? "All agents are sent. Pick some to send only those." : "Only the selected agents are sent."}
              </p>
            </div>
          </Section>
        </div>

        <div className="space-y-8">
          <Section title="Filters" hint="Applied in order to this target's copy only. Other targets are unaffected.">
            <div className="flex flex-wrap gap-2">
              {(info?.presets ?? []).map((p) => (
                <button key={p.id} className="btn-ghost py-1.5 text-xs" title={p.description} onClick={() => setRules([...rules, ...p.rules])}>
                  <Sparkles className="size-3.5 text-lime" /> {p.name}
                </button>
              ))}
            </div>
            {rules.length === 0 && (
              <div className="rounded-xl border border-dashed border-line px-4 py-6 text-center text-sm text-faint">
                Everything is forwarded as is. Start from a preset or add a rule.
              </div>
            )}
            <div className="space-y-2">
              {rules.map((r, i) => (
                <RuleEditor
                  key={i}
                  rule={r}
                  onChange={(next) => setRules(rules.map((x, j) => (j === i ? next : x)))}
                  onRemove={() => setRules(rules.filter((_, j) => j !== i))}
                  onMove={(d) => {
                    const next = [...rules]
                    const j = i + d
                    if (j < 0 || j >= next.length) return
                    ;[next[i], next[j]] = [next[j], next[i]]
                    setRules(next)
                  }}
                />
              ))}
            </div>
            <AddRule onAdd={(type) => setRules([...rules, emptyRule(type)])} />
          </Section>

          <Preview target={t} />
        </div>
      </div>
    </Modal>
  )
}

function emptyRule(type: RuleType): Rule {
  const r: Rule = { type }
  r[RULES[type].field] = [""]
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
    <div className="rounded-xl border border-line bg-raised/50 p-3">
      <div className="mb-2 flex items-center gap-2">
        <Wand2 className="size-3.5 text-lime" />
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
      <textarea
        className="input min-h-16 font-mono text-xs"
        rows={Math.min(6, Math.max(2, values.length))}
        placeholder={meta.help}
        value={values.join("\n")}
        onChange={(e) => onChange({ ...rule, [meta.field]: e.target.value.split("\n") })}
      />
      <div className="mt-2 flex flex-wrap items-center gap-4 text-xs text-muted">
        <span className="text-faint">One per line. {meta.help}.</span>
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

function IconBtn({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button aria-label={label} title={label} onClick={onClick} className="rounded p-1 hover:bg-line hover:text-fg">
      {children}
    </button>
  )
}

function AddRule({ onAdd }: { onAdd: (t: RuleType) => void }) {
  return (
    <select
      className="input w-auto text-sm"
      value=""
      onChange={(e) => {
        if (e.target.value) onAdd(e.target.value as RuleType)
      }}
    >
      <option value="">+ Add rule…</option>
      {(Object.keys(RULES) as RuleType[]).map((k) => (
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
            {result.beforeItems} → <span className="text-lime">{result.afterItems}</span> items
          </span>
        )}
      </div>
      <ErrorBanner message={error} />
      {result && !result.available && <p className="text-xs text-faint">No {signal} received yet. Send some telemetry first.</p>}
      {result?.available && (
        <div className="grid gap-2">
          <JsonPane title="Received" value={result.before} />
          <JsonPane title="Sent to this target" value={result.after} highlight />
        </div>
      )}
    </Section>
  )
}

function JsonPane({ title, value, highlight }: { title: string; value: unknown; highlight?: boolean }) {
  return (
    <div>
      <div className={`mb-1 text-[11px] tracking-wide uppercase ${highlight ? "text-lime" : "text-faint"}`}>{title}</div>
      <pre className="code max-h-64 overflow-auto">{JSON.stringify(value, null, 2)}</pre>
    </div>
  )
}
