import { useEffect, useState, type ReactNode } from "react"
import { Check, Copy, X } from "lucide-react"

export const GITHUB_URL = "https://github.com/webrix-ai/otlp-router"
export const WILLOW_URL = "https://withwillow.ai"

export function GithubIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" className={className} aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  )
}

export function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-3">
      <div className="grid size-9 place-items-center rounded-xl border border-lime/20 bg-lime/10 shadow-[0_0_24px_-6px_rgb(197_255_89/0.5)]">
        <img src="/willow-logo-green.svg" alt="" className="size-5" />
      </div>
      {!compact && (
        <div className="leading-tight">
          <div className="text-[15px] font-semibold tracking-tight">OTLP Router</div>
          <a href={WILLOW_URL} target="_blank" rel="noreferrer" className="text-[11px] text-muted hover:text-lime">
            by Willow
          </a>
        </div>
      )}
    </div>
  )
}

export function Toggle({
  checked,
  onChange,
  disabled,
  label
}: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  label?: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition disabled:opacity-50 ${
        checked ? "border-lime/50 bg-lime" : "border-line-strong bg-ink"
      }`}
    >
      <span
        className={`inline-block size-4 rounded-full shadow transition ${
          checked ? "translate-x-6 bg-ink" : "translate-x-1 bg-muted"
        }`}
      />
    </button>
  )
}

export type Health = "ok" | "error" | "idle" | "off"

export function StatusDot({ health }: { health: Health }) {
  const color = {
    ok: "bg-mint shadow-[0_0_10px_rgb(74_187_133/0.8)]",
    error: "bg-danger shadow-[0_0_10px_rgb(255_122_107/0.7)]",
    idle: "bg-sage",
    off: "bg-faint"
  }[health]
  return <span className={`inline-block size-2 rounded-full ${color}`} />
}

export function Modal({
  title,
  subtitle,
  onClose,
  children,
  footer,
  wide
}: {
  title: string
  subtitle?: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  wide?: boolean
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose()
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-ink/70 backdrop-blur-sm" onMouseDown={onClose}>
      <div
        className={`flex h-full w-full flex-col border-l border-line bg-canvas shadow-2xl ${wide ? "max-w-5xl" : "max-w-xl"}`}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between border-b border-line px-6 py-5">
          <div>
            <h2 className="text-lg font-semibold">{title}</h2>
            {subtitle && <p className="mt-0.5 text-sm text-muted">{subtitle}</p>}
          </div>
          <button className="rounded-lg p-1.5 text-muted hover:bg-raised hover:text-fg" onClick={onClose} aria-label="Close">
            <X className="size-5" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-6 py-5">{children}</div>
        {footer && <div className="flex items-center justify-end gap-2 border-t border-line px-6 py-4">{footer}</div>}
      </div>
    </div>
  )
}

export function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      className="btn-ghost px-2.5 py-1.5 text-xs"
      onClick={async () => {
        await navigator.clipboard.writeText(text)
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      }}
    >
      {copied ? <Check className="size-3.5 text-lime" /> : <Copy className="size-3.5" />}
      {copied ? "Copied" : "Copy"}
    </button>
  )
}

export function PageHeader({ title, description, action }: { title: string; description?: string; action?: ReactNode }) {
  return (
    <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-sm text-muted">{description}</p>}
      </div>
      {action}
    </div>
  )
}

export function ErrorBanner({ message }: { message?: string }) {
  if (!message) return null
  return (
    <div className="mb-4 rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{message}</div>
  )
}
