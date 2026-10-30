import { useCallback, useEffect, useState, type ReactNode } from "react"
import { KeyRound, Plus, RefreshCw, ShieldAlert, Trash2 } from "lucide-react"
import { api, timeAgo, timeUntil, type IngestToken, type TokenCreatedResponse, type TokensResponse } from "../api"
import { useRouterData } from "../App"
import { CopyButton, ErrorBanner, Modal, PageHeader } from "../ui"

const GRACE_OPTIONS = [
  { seconds: 0, label: "Stop the old token now" },
  { seconds: 3600, label: "Keep the old token for 1 hour" },
  { seconds: 86400, label: "Keep the old token for 24 hours" },
  { seconds: 7 * 86400, label: "Keep the old token for 7 days" }
]

type Dialog = { kind: "create" } | { kind: "rotate"; token: IngestToken } | { kind: "secret"; name: string; secret: string; rotated: boolean }

export default function Tokens() {
  const { info, reloadTargets, setFreshToken } = useRouterData()
  const [data, setData] = useState<TokensResponse>()
  const [dialog, setDialog] = useState<Dialog>()
  const [error, setError] = useState<string>()
  const readOnly = info?.readOnly ?? false

  const load = useCallback(() => api.tokens().then(setData), [])

  useEffect(() => {
    load().catch((e) => setError((e as Error).message))
    const timer = setInterval(() => load().catch(() => {}), 10000)
    return () => clearInterval(timer)
  }, [load])

  // Token changes bump the version the target editor saves against.
  const applied = (r: TokensResponse) => {
    setData(r)
    reloadTargets().catch(() => {})
  }

  const revealed = (r: TokenCreatedResponse, rotated: boolean) => {
    applied(r)
    setFreshToken({ name: r.created.name, secret: r.secret })
    setDialog({ kind: "secret", name: r.created.name, secret: r.secret, rotated })
  }

  const revoke = async (t: IngestToken) => {
    if (!window.confirm(`Revoke "${t.name}"? Agents still using it will be rejected right away.`)) return
    setError(undefined)
    try {
      applied(await api.revokeToken(t.id))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const tokens = data?.tokens ?? []
  const noTokens = data && !data.open && tokens.length === 0 && data.configTokens === 0

  return (
    <>
      <PageHeader
        title="Ingest tokens"
        description="Agents send one of these as Authorization: Bearer <token>. Only a hash is stored, so each token is shown once, when it's created."
        action={
          !readOnly && (
            <button className="btn-primary" onClick={() => setDialog({ kind: "create" })}>
              <Plus className="size-4" /> New token
            </button>
          )
        }
      />
      <ErrorBanner message={error} />

      {data?.open && (
        <Notice>
          Ingest is open: <code className="font-mono">auth.allowUnauthenticatedIngest</code> is on, so the router accepts telemetry with
          or without a token. Turn it off in the config to enforce these tokens.
        </Notice>
      )}
      {noTokens && (
        <Notice>No tokens yet, so the router rejects all telemetry. Create one and add it to your agents' OTLP headers.</Notice>
      )}

      {data && (tokens.length > 0 || data.configTokens > 0) && (
        <div className="card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="border-b border-line text-left text-[11px] tracking-wide text-faint uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">Name</th>
                <th className="px-5 py-3 font-medium">Token</th>
                <th className="px-5 py-3 font-medium">Created</th>
                <th className="px-5 py-3 font-medium">Last used</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {tokens.map((t) => (
                <tr key={t.id}>
                  <td className="px-5 py-3.5 font-medium">{t.name}</td>
                  <td className="px-5 py-3.5 font-mono text-xs text-muted">{t.hint}…</td>
                  <td className="px-5 py-3.5 text-muted">{timeAgo(t.createdAt)}</td>
                  <td className="px-5 py-3.5 text-muted" title="As seen by the replica serving this page">
                    {timeAgo(t.lastUsedAt)}
                  </td>
                  <td className="px-5 py-3.5">
                    {t.expiresAt ? (
                      <span className="chip border-warn/30 text-warn">Expires in {timeUntil(t.expiresAt)}</span>
                    ) : (
                      <span className="chip border-mint/30 text-mint">Active</span>
                    )}
                  </td>
                  <td className="px-5 py-3.5">
                    {!readOnly && (
                      <div className="flex justify-end gap-1.5">
                        {!t.expiresAt && (
                          <button className="btn-ghost py-1.5 text-xs" onClick={() => setDialog({ kind: "rotate", token: t })}>
                            <RefreshCw className="size-3.5" /> Rotate
                          </button>
                        )}
                        <button className="btn-ghost py-1.5 text-xs hover:text-danger" onClick={() => revoke(t)}>
                          <Trash2 className="size-3.5" /> Revoke
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
              {data.configTokens > 0 && (
                <tr className="text-muted">
                  <td className="px-5 py-3.5">From config</td>
                  <td className="px-5 py-3.5 text-xs" colSpan={4}>
                    {data.configTokens} token{data.configTokens > 1 ? "s" : ""} set in <code className="font-mono">auth.ingestTokens</code>.
                    Rotate {data.configTokens > 1 ? "them" : "it"} in your Helm values or Secret.
                  </td>
                  <td />
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {data && (
        <p className="mt-4 text-xs text-faint">
          {data.rejected > 0
            ? `${data.rejected.toLocaleString()} export request${data.rejected > 1 ? "s were" : " was"} rejected for a missing, unknown or expired token since this replica started.`
            : "No requests rejected since this replica started."}{" "}
          Changes reach every replica within a few seconds.
        </p>
      )}

      {dialog?.kind === "create" && <CreateDialog onClose={() => setDialog(undefined)} onCreated={(r) => revealed(r, false)} />}
      {dialog?.kind === "rotate" && (
        <RotateDialog token={dialog.token} onClose={() => setDialog(undefined)} onRotated={(r) => revealed(r, true)} />
      )}
      {dialog?.kind === "secret" && <SecretDialog {...dialog} onClose={() => setDialog(undefined)} />}
    </>
  )
}

function Notice({ children }: { children: ReactNode }) {
  return (
    <div className="mb-6 flex items-start gap-3 rounded-xl border border-warn/30 bg-warn/10 px-4 py-3 text-sm text-warn">
      <ShieldAlert className="mt-0.5 size-4 shrink-0" />
      <div>{children}</div>
    </div>
  )
}

function CreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (r: TokenCreatedResponse) => void }) {
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      onCreated(await api.createToken(name))
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal
      title="New ingest token"
      subtitle="Use one token per group of agents, so you can rotate or revoke it without touching the others."
      onClose={onClose}
      footer={
        <>
          <button className="btn-ghost" onClick={onClose}>
            Cancel
          </button>
          <button className="btn-primary" disabled={busy || !name.trim()} onClick={submit}>
            <KeyRound className="size-4" /> Create token
          </button>
        </>
      }
    >
      <ErrorBanner message={error} />
      <label className="label" htmlFor="token-name">
        Name
      </label>
      <input
        id="token-name"
        className="input"
        autoFocus
        maxLength={64}
        value={name}
        placeholder="Engineering laptops"
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && name.trim() && submit()}
      />
    </Modal>
  )
}

function RotateDialog({
  token,
  onClose,
  onRotated
}: {
  token: IngestToken
  onClose: () => void
  onRotated: (r: TokenCreatedResponse) => void
}) {
  const [grace, setGrace] = useState(86400)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      onRotated(await api.rotateToken(token.id, grace))
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal
      title={`Rotate "${token.name}"`}
      subtitle="Creates a new token with the same name. Roll it out to your agents before the old one stops working."
      onClose={onClose}
      footer={
        <>
          <button className="btn-ghost" onClick={onClose}>
            Cancel
          </button>
          <button className="btn-primary" disabled={busy} onClick={submit}>
            <RefreshCw className="size-4" /> Rotate
          </button>
        </>
      }
    >
      <ErrorBanner message={error} />
      <div className="space-y-2">
        {GRACE_OPTIONS.map((o) => (
          <label
            key={o.seconds}
            className={`flex cursor-pointer items-center gap-3 rounded-xl border px-4 py-3 text-sm transition ${
              grace === o.seconds ? "border-lime/50 bg-lime/10" : "border-line hover:border-line-strong"
            }`}
          >
            <input type="radio" name="grace" className="accent-lime" checked={grace === o.seconds} onChange={() => setGrace(o.seconds)} />
            {o.label}
          </label>
        ))}
      </div>
    </Modal>
  )
}

function SecretDialog({ name, secret, rotated, onClose }: { name: string; secret: string; rotated: boolean; onClose: () => void }) {
  return (
    <Modal
      title={rotated ? `"${name}" rotated` : `"${name}" created`}
      subtitle="Copy the token now. It won't be shown again."
      onClose={onClose}
      footer={
        <>
          <button className="btn-ghost" onClick={onClose}>
            Done
          </button>
          <button
            className="btn-primary"
            onClick={() => {
              onClose()
              window.location.hash = "/sources"
            }}
          >
            Set up an agent with it
          </button>
        </>
      }
    >
      <div className="relative">
        <pre className="code pr-24 break-all whitespace-pre-wrap">{secret}</pre>
        <div className="absolute top-3 right-3">
          <CopyButton text={secret} />
        </div>
      </div>
      <p className="mt-4 text-sm text-muted">
        Agents send it as <code className="font-mono text-fg">Authorization: Bearer {secret.slice(0, 10)}…</code>. The setup
        snippets on the Agents page use it until you reload this page.
      </p>
    </Modal>
  )
}
