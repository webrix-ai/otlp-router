import { useEffect, useState, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { ChevronRight, ChevronsDownUp, ChevronsUpDown, Maximize2, X } from "lucide-react"
import { CopyButton } from "./ui"

type Expansion = { depth: number; gen: number }

export function JsonViewer({ title, value, highlight }: { title: string; value: unknown; highlight?: boolean }) {
  const [expansion, setExpansion] = useState<Expansion>({ depth: 5, gen: 0 })
  const [open, setOpen] = useState(false)
  return (
    <div>
      <div className="mb-1 flex items-center gap-1">
        <span className={`text-[11px] tracking-wide uppercase ${highlight ? "text-primary" : "text-faint"}`}>{title}</span>
        <span className="ml-auto flex gap-0.5 text-faint">
          <ExpandButtons onChange={(depth) => setExpansion((e) => ({ depth, gen: e.gen + 1 }))} />
          <ToolBtn label="Open in full view" onClick={() => setOpen(true)}>
            <Maximize2 className="size-3.5" />
          </ToolBtn>
        </span>
      </div>
      <div className="code max-h-64 overflow-auto">
        <JsonTree key={expansion.gen} value={value} depth={expansion.depth} />
      </div>
      {open && <JsonDialog title={title} value={value} onClose={() => setOpen(false)} />}
    </div>
  )
}

function JsonDialog({ title, value, onClose }: { title: string; value: unknown; onClose: () => void }) {
  const [expansion, setExpansion] = useState<Expansion>({ depth: Infinity, gen: 0 })

  // Capture phase on window runs before the bubble-phase Escape handler of the
  // editor drawer underneath, so Escape closes only this dialog.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return
      e.stopPropagation()
      onClose()
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [onClose])

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-overlay p-6 backdrop-blur-sm"
      onMouseDown={(e) => {
        e.stopPropagation()
        onClose()
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="flex h-full max-h-[90vh] w-full max-w-5xl flex-col rounded-2xl border border-line bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-line px-5 py-3">
          <h2 className="text-sm font-semibold">{title}</h2>
          <span className="ml-auto flex items-center gap-1 text-muted">
            <ExpandButtons onChange={(depth) => setExpansion((e) => ({ depth, gen: e.gen + 1 }))} />
            <CopyButton text={JSON.stringify(value, null, 2)} />
            <button className="rounded-lg p-1.5 hover:bg-raised hover:text-fg" onClick={onClose} aria-label="Close">
              <X className="size-5" />
            </button>
          </span>
        </div>
        <div className="flex-1 overflow-auto bg-well p-5">
          <JsonTree key={expansion.gen} value={value} depth={expansion.depth} />
        </div>
      </div>
    </div>,
    document.body
  )
}

function ExpandButtons({ onChange }: { onChange: (depth: number) => void }) {
  return (
    <>
      <ToolBtn label="Expand all" onClick={() => onChange(Infinity)}>
        <ChevronsUpDown className="size-3.5" />
      </ToolBtn>
      <ToolBtn label="Collapse all" onClick={() => onChange(1)}>
        <ChevronsDownUp className="size-3.5" />
      </ToolBtn>
    </>
  )
}

function ToolBtn({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" aria-label={label} title={label} onClick={onClick} className="rounded p-1 hover:bg-line hover:text-fg">
      {children}
    </button>
  )
}

export function JsonTree({ value, depth }: { value: unknown; depth: number }) {
  return (
    <div className="font-mono text-xs leading-relaxed text-fg/90">
      <JsonNode value={value} level={0} depth={depth} last />
    </div>
  )
}

function JsonNode({ name, value, level, depth, last }: { name?: string; value: unknown; level: number; depth: number; last: boolean }) {
  const [expanded, setExpanded] = useState(level < depth)
  const comma = last ? "" : ","
  const label = name !== undefined && (
    <>
      <span className="text-json-key">"{name}"</span>
      <span className="text-faint">: </span>
    </>
  )

  if (value === null || typeof value !== "object") {
    return (
      <Line>
        {label}
        <Scalar value={value} />
        {comma}
      </Line>
    )
  }

  const isArray = Array.isArray(value)
  const entries: [string | undefined, unknown][] = isArray ? value.map((v) => [undefined, v]) : Object.entries(value)
  const [open, close] = isArray ? ["[", "]"] : ["{", "}"]

  if (entries.length === 0) {
    return (
      <Line>
        {label}
        {open}
        {close}
        {comma}
      </Line>
    )
  }

  const toggle = (
    <button
      type="button"
      aria-label={expanded ? "Collapse" : "Expand"}
      aria-expanded={expanded}
      onClick={() => setExpanded(!expanded)}
      className="grid size-4 place-items-center rounded text-faint hover:bg-line hover:text-fg"
    >
      <ChevronRight className={`size-3 transition-transform ${expanded ? "rotate-90" : ""}`} />
    </button>
  )

  if (!expanded) {
    const count = `${entries.length} ${isArray ? (entries.length === 1 ? "item" : "items") : entries.length === 1 ? "key" : "keys"}`
    return (
      <Line toggle={toggle}>
        {label}
        <button type="button" onClick={() => setExpanded(true)} className="rounded px-0.5 hover:bg-line" title="Expand">
          {open}
          <span className="text-faint"> {summary(value) || "…"} </span>
          {close}
        </button>
        {comma}
        <span className="ml-2 text-[10px] text-faint">{count}</span>
      </Line>
    )
  }

  return (
    <div>
      <Line toggle={toggle}>
        {label}
        {open}
      </Line>
      <div className="ml-[7px] border-l border-line pl-[9px]">
        {entries.map(([k, v], i) => (
          <JsonNode key={k ?? i} name={k} value={v} level={level + 1} depth={depth} last={i === entries.length - 1} />
        ))}
      </div>
      <Line>
        {close}
        {comma}
      </Line>
    </div>
  )
}

function Line({ toggle, children }: { toggle?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex items-start">
      <span className="mt-0.5 w-4 shrink-0">{toggle}</span>
      <span className="min-w-0 break-all whitespace-pre-wrap">{children}</span>
    </div>
  )
}

function Scalar({ value }: { value: unknown }) {
  if (typeof value === "string") return <span className="text-json-string">{JSON.stringify(value)}</span>
  if (typeof value === "number") return <span className="text-json-number">{value}</span>
  return <span className="text-json-literal">{String(value)}</span>
}

// summary previews a collapsed node's contents one level deep, e.g.
// key: "cowork.surface", value: { stringValue: "remote" }.
function summary(value: object, budget = 80): string {
  const isArray = Array.isArray(value)
  const parts: string[] = []
  let length = 0
  for (const [k, v] of Object.entries(value)) {
    let shown: string
    if (v === null || typeof v !== "object") shown = JSON.stringify(v)
    else if (budget < 80) continue
    else {
      const inner = summary(v, 40)
      if (!inner || inner.endsWith("…")) continue
      shown = Array.isArray(v) ? `[ ${inner} ]` : `{ ${inner} }`
    }
    const text = isArray ? shown : `${k}: ${shown}`
    if (length + text.length > budget) {
      parts.push("…")
      break
    }
    parts.push(text)
    length += text.length + 2
  }
  return parts.join(", ")
}
