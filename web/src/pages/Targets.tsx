import { useState } from "react"
import { ArrowRight, Filter, Pencil, Plus } from "lucide-react"
import { api, formatNumber, timeAgo, SIGNALS, willowEndpoint, type Target } from "../api"
import { agentName } from "../agents"
import { useRouterData } from "../App"
import { ErrorBanner, PageHeader, StatusDot, Toggle } from "../ui"
import { targetHealth } from "./Overview"
import TargetEditor, { newTarget } from "./TargetEditor"

const TYPE_LABEL = { willow: "Willow", otlphttp: "OTLP/HTTP", otlpgrpc: "OTLP/gRPC" }

export default function Targets() {
  const { targets, stats, version, applyTargets, info } = useRouterData()
  const [editing, setEditing] = useState<Target>()
  const [error, setError] = useState<string>()
  const readOnly = info?.readOnly ?? false
  const loaded = version !== ""
  const hasWillow = targets.some((t) => t.type === "willow")

  const toggle = async (t: Target, enabled: boolean) => {
    setError(undefined)
    try {
      applyTargets(await api.setEnabled(t.id, enabled, version))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <>
      <PageHeader
        title="Targets"
        description="Every payload is copied to each enabled target, filtered for that target, and delivered from its own queue."
        action={
          !readOnly && (
            <button className="btn-primary" onClick={() => setEditing(newTarget("otlphttp"))}>
              <Plus className="size-4" /> Add target
            </button>
          )
        }
      />
      <ErrorBanner message={error} />

      {loaded && !hasWillow && !readOnly && (
        <button
          onClick={() => setEditing(newTarget("willow"))}
          className="group mb-6 flex w-full items-center gap-5 overflow-hidden rounded-2xl border border-lime/25 bg-gradient-to-r from-forest/50 via-surface to-surface p-5 text-left transition hover:border-lime/50"
        >
          <div className="grid size-12 shrink-0 place-items-center rounded-xl bg-lime/15">
            <img src="/willow-logo-green.svg" alt="" className="size-6" />
          </div>
          <div className="flex-1">
            <div className="font-medium">Send to Willow</div>
            <div className="text-sm text-muted">
              Adoption, cost and risk analytics across every agent, per team. Filter prompts and identities before they leave.
            </div>
          </div>
          <span className="flex items-center gap-1 text-sm text-lime">
            Connect <ArrowRight className="size-4 transition group-hover:translate-x-0.5" />
          </span>
        </button>
      )}

      {!loaded ? null : targets.length === 0 ? (
        <div className="card px-6 py-16 text-center text-sm text-muted">No targets yet. Telemetry is received but not forwarded.</div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {targets.map((t) => {
            const s = stats?.targets[t.id]
            const health = targetHealth(t, s)
            return (
              <div key={t.id} className={`card flex flex-col p-5 transition ${t.enabled ? "" : "opacity-60"}`}>
                <div className="flex items-start gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <StatusDot health={health} />
                      <span className="truncate font-medium">{t.name}</span>
                      <span className="chip">
                        {t.type === "willow" && <img src="/willow-logo-green.svg" alt="" className="size-3" />}
                        {TYPE_LABEL[t.type]}
                      </span>
                    </div>
                    <div className="mt-1 truncate font-mono text-xs text-faint">
                      {t.type === "willow" ? willowEndpoint(t.willow).replace(/^https:\/\//, "") : t.endpoint}
                    </div>
                  </div>
                  <Toggle checked={t.enabled} disabled={readOnly} onChange={(v) => toggle(t, v)} label={`Enable ${t.name}`} />
                </div>

                <div className="mt-4 flex flex-wrap gap-1.5">
                  {SIGNALS.map((sig) => (
                    <span key={sig} className={`chip ${t.signals[sig] ? "border-mint/30 text-mint" : "line-through opacity-50"}`}>
                      {sig}
                    </span>
                  ))}
                  {(t.agents ?? []).length > 0 && <span className="chip">{t.agents!.map(agentName).join(", ")}</span>}
                  {(t.filters ?? []).length > 0 && (
                    <span className="chip border-lime/30 text-lime">
                      <Filter className="size-3" /> {t.filters!.length} filter{t.filters!.length > 1 ? "s" : ""}
                    </span>
                  )}
                </div>

                <div className="mt-5 grid grid-cols-4 gap-2 rounded-xl bg-ink/40 px-3 py-2.5 text-center">
                  <Stat label="sent" value={formatNumber(s?.sent)} />
                  <Stat label="failed" value={formatNumber(s?.failed)} danger={!!s?.failed} />
                  <Stat label="dropped" value={formatNumber(s?.dropped)} danger={!!s?.dropped} />
                  <Stat label="queue" value={`${s?.queueLen ?? 0}`} />
                </div>

                <div className="mt-4 flex items-center justify-between gap-3">
                  <span className={`truncate text-xs ${health === "error" ? "text-danger" : "text-faint"}`} title={s?.lastError}>
                    {health === "error" ? s?.lastError : `Last delivery ${timeAgo(s?.lastSuccessAt)}`}
                  </span>
                  <button className="btn-ghost py-1.5 text-xs" onClick={() => setEditing(t)}>
                    <Pencil className="size-3.5" /> {readOnly ? "View" : "Edit"}
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {editing && <TargetEditor initial={editing} onClose={() => setEditing(undefined)} />}
    </>
  )
}

function Stat({ label, value, danger }: { label: string; value: string; danger?: boolean }) {
  return (
    <div>
      <div className={`text-sm font-medium tabular-nums ${danger ? "text-danger" : ""}`}>{value}</div>
      <div className="text-[10px] tracking-wide text-faint uppercase">{label}</div>
    </div>
  )
}
