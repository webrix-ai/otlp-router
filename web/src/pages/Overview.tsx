import { useEffect, useRef, useState } from "react"
import { ArrowRight, Inbox } from "lucide-react"
import { SIGNALS, formatNumber, timeAgo, type Signal, type Target, type TargetStats } from "../api"
import { agentName } from "../agents"
import { useRouterData } from "../App"
import { PageHeader, StatusDot, type Health } from "../ui"

export function targetHealth(t: Target, s?: TargetStats): Health {
  if (!t.enabled) return "off"
  if (!s || (!s.lastSuccessAt && !s.lastErrorAt)) return "idle"
  if (s.lastErrorAt && (!s.lastSuccessAt || s.lastErrorAt > s.lastSuccessAt)) return "error"
  return "ok"
}

// useRates turns the cumulative counters into per-second rates between polls.
function useRates(items?: Record<Signal, number>): Record<Signal, number> {
  const prev = useRef<{ at: number; items: Record<Signal, number> } | null>(null)
  const [rates, setRates] = useState<Record<Signal, number>>({ traces: 0, metrics: 0, logs: 0 })
  useEffect(() => {
    if (!items) return
    const now = Date.now()
    if (prev.current) {
      const dt = (now - prev.current.at) / 1000
      if (dt > 0) {
        const next = {} as Record<Signal, number>
        for (const s of SIGNALS) next[s] = Math.max(0, (items[s] - prev.current.items[s]) / dt)
        setRates(next)
      }
    }
    prev.current = { at: now, items }
  }, [items])
  return rates
}

const SIGNAL_LABEL: Record<Signal, string> = { traces: "Spans", metrics: "Data points", logs: "Log records" }

export default function Overview() {
  const { stats, targets } = useRouterData()
  const rates = useRates(stats?.ingest.items)
  const agents = stats?.ingest.agents ?? []
  const active = targets.filter((t) => t.enabled).length

  return (
    <>
      <PageHeader title="Overview" description="Telemetry arriving at this router and where it goes." />

      <div className="grid gap-4 md:grid-cols-4">
        {SIGNALS.map((s) => (
          <div key={s} className="card p-5">
            <div className="text-xs tracking-wide text-muted uppercase">{SIGNAL_LABEL[s]}</div>
            <div className="mt-2 text-3xl font-semibold tabular-nums">{formatNumber(stats?.ingest.items[s])}</div>
            <div className="mt-1 text-xs text-faint tabular-nums">
              {rates[s] > 0 ? <span className="text-lime">{rates[s].toFixed(1)}/s</span> : "idle"} ·{" "}
              {formatNumber(stats?.ingest.batches[s])} requests
            </div>
          </div>
        ))}
        <div className="card flex flex-col justify-between bg-gradient-to-br from-lime/10 to-transparent p-5">
          <div className="text-xs tracking-wide text-muted uppercase">Targets</div>
          <div className="mt-2 text-3xl font-semibold tabular-nums">
            {active}
            <span className="text-lg text-faint"> / {targets.length}</span>
          </div>
          <a href="#/targets" className="mt-1 flex items-center gap-1 text-xs text-lime hover:underline">
            Manage targets <ArrowRight className="size-3" />
          </a>
        </div>
      </div>

      <div className="mt-8 grid gap-6 lg:grid-cols-[1.4fr_1fr]">
        <section className="card p-6">
          <h2 className="mb-4 font-medium">Delivery</h2>
          {targets.length === 0 ? (
            <Empty text="No targets yet. Add one to start forwarding." href="#/targets" />
          ) : (
            <div className="divide-y divide-line">
              {targets.map((t) => {
                const s = stats?.targets[t.id]
                return (
                  <div key={t.id} className="flex items-center gap-4 py-3">
                    <StatusDot health={targetHealth(t, s)} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{t.name}</div>
                      <div className="truncate text-xs text-faint">
                        {s?.lastError && targetHealth(t, s) === "error" ? (
                          <span className="text-danger">{s.lastError}</span>
                        ) : (
                          `last delivery ${timeAgo(s?.lastSuccessAt)}`
                        )}
                      </div>
                    </div>
                    <Metric label="sent" value={s?.sent} />
                    <Metric label="failed" value={s?.failed} danger />
                    <Metric label="queued" value={s?.queueLen} />
                  </div>
                )
              })}
            </div>
          )}
        </section>

        <section className="card p-6">
          <h2 className="mb-4 font-medium">Agents seen</h2>
          {agents.length === 0 ? (
            <Empty text="Nothing received yet. Connect an agent to this router." href="#/sources" />
          ) : (
            <div className="space-y-2">
              {agents.map((a) => (
                <div key={a.agent} className="flex items-center justify-between rounded-lg bg-raised/60 px-3 py-2.5">
                  <div>
                    <div className="text-sm font-medium">{agentName(a.agent)}</div>
                    <div className="text-xs text-faint">{timeAgo(a.lastSeenAt)}</div>
                  </div>
                  <div className="flex gap-1.5">
                    {SIGNALS.filter((s) => a.items[s]).map((s) => (
                      <span key={s} className="chip">
                        {s} {formatNumber(a.items[s])}
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </>
  )
}

function Metric({ label, value, danger }: { label: string; value?: number; danger?: boolean }) {
  return (
    <div className="w-16 text-right">
      <div className={`text-sm tabular-nums ${danger && value ? "text-danger" : ""}`}>{formatNumber(value)}</div>
      <div className="text-[10px] tracking-wide text-faint uppercase">{label}</div>
    </div>
  )
}

function Empty({ text, href }: { text: string; href: string }) {
  return (
    <a href={href} className="flex flex-col items-center gap-2 rounded-xl border border-dashed border-line py-10 text-sm text-muted hover:border-lime/40">
      <Inbox className="size-5 text-faint" />
      {text}
    </a>
  )
}
