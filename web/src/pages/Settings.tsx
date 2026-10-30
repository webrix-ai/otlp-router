import type { ReactNode } from "react"
import { useRouterData } from "../App"
import { PageHeader } from "../ui"
import { ingestURL } from "./Sources"

export default function Settings() {
  const { info, username, version } = useRouterData()
  const storeLabel: Record<string, string> = {
    configmap: "Kubernetes ConfigMap, shared by all replicas",
    file: "File on disk",
    memory: "In memory, lost on restart"
  }
  return (
    <>
      <PageHeader
        title="Settings"
        description="These come from the router's config file or Helm values and can't be changed here."
      />
      <div className="card divide-y divide-line">
        <Row label="Version" value={info?.version} />
        <Row label="Admin user" value={username} />
        <Row label="OTLP/HTTP ingress" value={<code className="font-mono">{ingestURL(info?.publicUrl)}</code>} />
        <Row label="Listeners" value={info && `gRPC ${info.listen.OTLPGRPC} · HTTP ${info.listen.OTLPHTTP} · UI ${info.listen.Admin}`} />
        <Row
          label="Ingest auth"
          value={info?.ingestAuth ? "Bearer token required" : <span className="text-warn">Disabled. Set auth.ingestTokens to require one.</span>}
        />
        <Row label="Target storage" value={info && (storeLabel[info.store] ?? info.store)} />
        <Row label="Target list version" value={<code className="font-mono text-xs">{version || "—"}</code>} />
        <Row label="Read-only mode" value={info?.readOnly ? "On" : "Off"} />
      </div>
    </>
  )
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6 px-6 py-4 text-sm">
      <span className="text-muted">{label}</span>
      <span className="text-right">{value ?? "—"}</span>
    </div>
  )
}
