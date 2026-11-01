import { useEffect, useRef, useState, type ReactNode } from "react"
import { ChevronRight, Pause, Play, Radio, Trash2 } from "lucide-react"
import { SIGNALS, api, formatNumber, type LiveEntry, type LiveTargetStatus, type PodsReport, type Signal } from "../api"
import { agentName } from "../agents"
import { useRouterData } from "../App"
import { ErrorBanner, PageHeader } from "../ui"
import { PodsNotice } from "./Overview"

const POLL_MS = 2000
const KEEP = 300
// Replica clocks differ a little, so each poll asks again for the last few seconds.
const OVERLAP_MS = 5000

const STATUS_STYLE: Record<LiveTargetStatus, string> = {
  sent: "border-success/40 bg-success/10 text-success",
  queued: "border-line bg-well text-muted",
  retrying: "border-warn/40 bg-warn/10 text-warn",
  filtered: "border-line bg-well text-faint",
  dropped: "border-danger/40 bg-danger/10 text-danger",
  failed: "border-danger/40 bg-danger/10 text-danger"
}

const NAMES_LABEL: Record<Signal, string> = { traces: "Span names", metrics: "Metric names", logs: "Event names" }

function hasProblem(e: LiveEntry): boolean {
  return e.status === "rejected" || (e.targets ?? []).some((t) => ["failed", "dropped", "retrying"].includes(t.status))
}

function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })
}

function useLive(paused: boolean) {
  const [entries, setEntries] = useState<LiveEntry[]>([])
  const [pods, setPods] = useState<PodsReport>()
  const [error, setError] = useState<string>()
  const byId = useRef(new Map<string, LiveEntry>())
  const since = useRef(0)
  const clearedAt = useRef(0)

  useEffect(() => {
    if (paused) return
    let stopped = false
    const poll = async () => {
      if (document.hidden) return
      try {
        const res = await api.live(since.current)
        if (stopped) return
        for (const e of res.entries) {
          if (e.time > clearedAt.current) byId.current.set(e.id, e)
          since.current = Math.max(since.current, e.updatedAt - OVERLAP_MS)
        }
        const next = [...byId.current.values()].sort((a, b) => b.time - a.time).slice(0, KEEP)
        byId.current = new Map(next.map((e) => [e.id, e]))
        setEntries(next)
        setPods(res.pods)
        setError(undefined)
      } catch (err) {
        if (!stopped) setError((err as Error).message)
      }
    }
    poll()
    const timer = setInterval(poll, POLL_MS)
    return () => {
      stopped = true
      clearInterval(timer)
    }
  }, [paused])

  const clear = () => {
    clearedAt.current = Date.now()
    byId.current.clear()
    setEntries([])
  }
  return { entries, pods, error, clear }
}

export default function Live() {
  const { targets } = useRouterData()
  const [paused, setPaused] = useState(false)
  const [signal, setSignal] = useState<Signal | "all">("all")
  const [problemsOnly, setProblemsOnly] = useState(false)
  const { entries, pods, error, clear } = useLive(paused)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })

  const targetName = (id: string) => targets.find((t) => t.id === id)?.name ?? id
  const order = (id: string) => {
    const i = targets.findIndex((t) => t.id === id)
    return i < 0 ? targets.length : i
  }
  const shown = entries
    .filter((e) => (signal === "all" || e.signal === signal) && (!problemsOnly || hasProblem(e)))
    .map((e) => ({ ...e, targets: [...(e.targets ?? [])].sort((a, b) => order(a.id) - order(b.id)) }))

  return (
    <>
      <PageHeader
        title="Live"
        description="The latest requests agents sent to this router and what each target did with them. Only who sent what and where it went is kept, never the telemetry itself, and only in memory."
        action={
          <div className="flex gap-2">
            <button className="btn-ghost" onClick={() => setPaused(!paused)}>
              {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
              {paused ? "Resume" : "Pause"}
            </button>
            <button className="btn-ghost" onClick={clear}>
              <Trash2 className="size-4" /> Clear
            </button>
          </div>
        }
      />
      <PodsNotice pods={pods} />
      <ErrorBanner message={error} />

      <div className="mb-4 flex flex-wrap items-center gap-2 text-sm">
        {(["all", ...SIGNALS] as const).map((s) => (
          <button
            key={s}
            onClick={() => setSignal(s)}
            className={`chip ${signal === s ? "border-primary/50 bg-primary/10 text-fg" : "hover:text-fg"}`}
          >
            {s}
          </button>
        ))}
        <label className="ml-auto flex items-center gap-2 text-xs text-muted">
          <input type="checkbox" checked={problemsOnly} onChange={(e) => setProblemsOnly(e.target.checked)} />
          Problems only
        </label>
      </div>

      <section className="card divide-y divide-line">
        {shown.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-12 text-sm text-muted">
            <Radio className={`size-5 text-faint ${paused ? "" : "animate-pulse"}`} />
            {paused ? "Paused." : "Waiting for requests."}
          </div>
        ) : (
          shown.map((e) => (
            <Row key={e.id} entry={e} targetName={targetName} open={expanded.has(e.id)} onToggle={() => toggle(e.id)} />
          ))
        )}
      </section>
    </>
  )
}

function Row({
  entry: e,
  targetName,
  open,
  onToggle
}: {
  entry: LiveEntry
  targetName: (id: string) => string
  open: boolean
  onToggle: () => void
}) {
  const agents = Object.keys(e.agents ?? {})
  const errors = (e.targets ?? []).filter((t) => t.error && t.status !== "sent")
  return (
    <div className={`text-sm ${open ? "bg-well/60" : ""}`}>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className="flex w-full flex-wrap items-center gap-x-4 gap-y-1.5 px-5 pt-3 text-left hover:bg-well/60"
      >
        <ChevronRight className={`-ml-1 size-4 shrink-0 text-faint transition-transform ${open ? "rotate-90" : ""}`} />
        <span className="w-20 font-mono text-xs text-faint tabular-nums">{clock(e.time)}</span>
        <span className="chip w-20 justify-center">{e.signal || "unknown"}</span>
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium">
            {e.status === "rejected" ? (
              <span className="text-danger">Rejected</span>
            ) : agents.length ? (
              agents.map(agentName).join(", ")
            ) : (
              "Unknown agent"
            )}
            {e.status === "accepted" && <span className="ml-2 text-xs font-normal text-faint">{formatNumber(e.items)} items</span>}
          </div>
          <div className="truncate text-xs text-faint">
            {[e.user, e.device, e.token && `${e.token} token`].filter(Boolean).join(" · ") || "no sender details"}
          </div>
        </div>
        <div className="flex flex-wrap justify-end gap-1.5">
          {(e.targets ?? []).map((t) => (
            <span
              key={t.id}
              title={t.error}
              className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs ${STATUS_STYLE[t.status]}`}
            >
              {targetName(t.id)}: {t.status}
            </span>
          ))}
          {e.status === "accepted" && !e.targets?.length && <span className="text-xs text-faint">no target took it</span>}
        </div>
        <div className="w-full pb-3 pl-28">
          {!open && e.error && <div className="truncate text-xs text-danger">{e.error}</div>}
          {!open &&
            errors.map((t) => (
              <div key={t.id} className="mt-1 truncate text-xs text-danger">
                {targetName(t.id)}: {t.error}
              </div>
            ))}
        </div>
      </button>
      {open && <Details entry={e} targetName={targetName} />}
    </div>
  )
}

function Details({ entry: e, targetName }: { entry: LiveEntry; targetName: (id: string) => string }) {
  const names = Object.entries(e.names ?? {}).sort((a, b) => b[1] - a[1])
  const agents = Object.entries(e.agents ?? {}).sort((a, b) => b[1] - a[1])
  const fields: [string, ReactNode][] = [
    ["Received", new Date(e.time).toLocaleString()],
    ["Last update", new Date(e.updatedAt).toLocaleString()],
    ["Signal", e.signal || "unknown"],
    ["Status", e.status === "rejected" ? <span className="text-danger">rejected</span> : "accepted"],
    ["Items", formatNumber(e.items)],
    ["User", e.user],
    ["Device", e.device],
    ["Ingest token", e.token],
    ["Request id", <span className="font-mono">{e.id}</span>]
  ]
  return (
    <div className="space-y-5 px-5 pt-1 pb-5 pl-28">
      {e.error && <p className="text-xs break-words text-danger">{e.error}</p>}

      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-xs md:grid-cols-3">
        {fields.map(([label, value]) => (
          <div key={label}>
            <dt className="label mb-0.5">{label}</dt>
            <dd className="break-words text-fg">{value || <span className="text-faint">not sent</span>}</dd>
          </div>
        ))}
      </dl>

      {agents.length > 0 && (
        <div>
          <div className="label">Agents</div>
          <div className="flex flex-wrap gap-1.5">
            {agents.map(([a, n]) => (
              <span key={a} className="chip">
                {agentName(a)} {formatNumber(n)}
              </span>
            ))}
          </div>
        </div>
      )}

      {names.length > 0 && (
        <div>
          <div className="label">{NAMES_LABEL[e.signal || "logs"]}</div>
          <div className="flex flex-wrap gap-1.5">
            {names.map(([n, c]) => (
              <span key={n} className="chip font-mono">
                {n} <span className="text-faint">×{formatNumber(c)}</span>
              </span>
            ))}
          </div>
        </div>
      )}

      {(e.targets ?? []).length > 0 && (
        <div>
          <div className="label">Targets</div>
          <table className="w-full text-left text-xs">
            <thead className="text-faint">
              <tr>
                <th className="py-1 pr-4 font-medium">Target</th>
                <th className="py-1 pr-4 font-medium">Status</th>
                <th className="py-1 pr-4 font-medium">Items</th>
                <th className="py-1 font-medium">Error</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {e.targets!.map((t) => (
                <tr key={t.id} className="align-top">
                  <td className="py-1.5 pr-4 whitespace-nowrap">{targetName(t.id)}</td>
                  <td className="py-1.5 pr-4">
                    <span className={`inline-flex rounded-full border px-2 py-0.5 ${STATUS_STYLE[t.status]}`}>{t.status}</span>
                  </td>
                  <td className="py-1.5 pr-4 tabular-nums">{t.status === "filtered" ? "0" : formatNumber(t.items)}</td>
                  <td className="py-1.5 break-all text-danger">{t.error}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="text-[11px] text-faint">
        The router keeps only these details. The telemetry itself is forwarded and never stored.
      </p>
    </div>
  )
}
