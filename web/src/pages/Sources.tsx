import { useState } from "react"
import { CheckCircle2, Circle } from "lucide-react"
import { AGENTS } from "../agents"
import { useRouterData } from "../App"
import { CopyButton, PageHeader } from "../ui"

export function ingestURL(publicUrl?: string): string {
  if (publicUrl) return publicUrl.replace(/\/+$/, "")
  return `${window.location.protocol}//${window.location.hostname}:4318`
}

export default function Sources() {
  const { info, stats } = useRouterData()
  const [selected, setSelected] = useState(AGENTS[0].slug)
  const url = ingestURL(info?.publicUrl)
  const token = info?.ingestAuth ? "<ingest-token>" : ""
  const seen = new Set((stats?.ingest.agents ?? []).map((a) => a.agent))
  const agent = AGENTS.find((a) => a.slug === selected)!
  const snippet = agent.snippet(url, token)

  return (
    <>
      <PageHeader
        title="Agents"
        description="Point each agent's built-in OpenTelemetry export at this router. Every OTLP exporter works: gRPC on 4317 or HTTP on 4318, protobuf or JSON."
      />

      <div className="card mb-6 flex flex-wrap items-center gap-6 p-5 text-sm">
        <div>
          <div className="label">OTLP/HTTP endpoint</div>
          <code className="font-mono text-lime">{url}</code>
        </div>
        <div>
          <div className="label">Ingest auth</div>
          <span className={info?.ingestAuth ? "text-mint" : "text-warn"}>
            {info?.ingestAuth ? "Bearer token required" : "Open: anyone on the network can send"}
          </span>
        </div>
        {!info?.publicUrl && (
          <p className="text-xs text-faint">Set publicUrl in the config to show your ingress address here.</p>
        )}
      </div>

      <div className="grid gap-6 lg:grid-cols-[260px_1fr]">
        <div className="space-y-1.5">
          {AGENTS.map((a) => (
            <button
              key={a.slug}
              onClick={() => setSelected(a.slug)}
              className={`flex w-full items-center gap-3 rounded-xl border px-3 py-2.5 text-left transition ${
                selected === a.slug ? "border-lime/50 bg-lime/10" : "border-line hover:border-line-strong"
              }`}
            >
              {seen.has(a.slug) ? <CheckCircle2 className="size-4 text-mint" /> : <Circle className="size-4 text-faint" />}
              <div className="min-w-0">
                <div className="text-sm font-medium">{a.name}</div>
                <div className="truncate text-[11px] text-faint">{a.vendor}</div>
              </div>
            </button>
          ))}
        </div>

        <div className="card p-6">
          <div className="flex items-start justify-between gap-4">
            <div>
              <h2 className="text-lg font-medium">{agent.name}</h2>
              <p className="mt-1 text-sm text-muted">{agent.where}</p>
            </div>
            <div className="flex gap-1.5">
              {agent.signals.map((s) => (
                <span key={s} className="chip">
                  {s}
                </span>
              ))}
            </div>
          </div>
          <div className="relative mt-5">
            <pre className="code overflow-x-auto pr-24">{snippet}</pre>
            <div className="absolute top-3 right-3">
              <CopyButton text={snippet} />
            </div>
          </div>
          {agent.note && <p className="mt-3 text-xs text-faint">{agent.note}</p>}
          <p className="mt-6 text-xs text-faint">
            {seen.has(agent.slug) ? "Receiving telemetry from this agent." : "Waiting for the first export from this agent."}
          </p>
        </div>
      </div>
    </>
  )
}
