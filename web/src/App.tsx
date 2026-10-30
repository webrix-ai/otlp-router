import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react"
import { Activity, BookOpen, KeyRound, LogOut, Radio, Settings as SettingsIcon, Waypoints } from "lucide-react"
import { api, type Info, type Stats, type Target, type TargetsResponse } from "./api"
import { Brand, GITHUB_URL, GithubIcon, WILLOW_URL } from "./ui"
import Login from "./pages/Login"
import Overview from "./pages/Overview"
import Targets from "./pages/Targets"
import Sources from "./pages/Sources"
import Tokens from "./pages/Tokens"
import Settings from "./pages/Settings"

/** A token created in this browser session. Kept in memory only, so the agent snippets can use it. */
export interface FreshToken {
  name: string
  secret: string
}

interface RouterData {
  info?: Info
  targets: Target[]
  version: string
  stats?: Stats
  username: string
  applyTargets: (r: TargetsResponse) => void
  reloadTargets: () => Promise<void>
  freshToken?: FreshToken
  setFreshToken: (t?: FreshToken) => void
}

const DataContext = createContext<RouterData | null>(null)

export function useRouterData(): RouterData {
  const ctx = useContext(DataContext)
  if (!ctx) throw new Error("useRouterData outside provider")
  return ctx
}

const PAGES = [
  { id: "overview", label: "Overview", icon: Activity },
  { id: "targets", label: "Targets", icon: Waypoints },
  { id: "sources", label: "Agents", icon: Radio },
  { id: "tokens", label: "Tokens", icon: KeyRound },
  { id: "settings", label: "Settings", icon: SettingsIcon }
] as const

type PageId = (typeof PAGES)[number]["id"]

function usePage(): [PageId, (p: PageId) => void] {
  const read = () => {
    const id = window.location.hash.replace(/^#\/?/, "")
    return (PAGES.some((p) => p.id === id) ? id : "overview") as PageId
  }
  const [page, setPage] = useState<PageId>(read)
  useEffect(() => {
    const onHash = () => setPage(read())
    window.addEventListener("hashchange", onHash)
    return () => window.removeEventListener("hashchange", onHash)
  }, [])
  return [page, (p) => (window.location.hash = `/${p}`)]
}

export default function App() {
  const [session, setSession] = useState<{ checked: boolean; username: string }>({ checked: false, username: "" })

  useEffect(() => {
    api
      .session()
      .then((s) => setSession({ checked: true, username: s.authenticated ? s.username : "" }))
      .catch(() => setSession({ checked: true, username: "" }))
    const onLogout = () => setSession({ checked: true, username: "" })
    window.addEventListener("otlp-router:logout", onLogout)
    return () => window.removeEventListener("otlp-router:logout", onLogout)
  }, [])

  if (!session.checked) return null
  if (!session.username) return <Login onLogin={(username) => setSession({ checked: true, username })} />
  return (
    <DataProvider username={session.username}>
      <Shell onLogout={() => setSession({ checked: true, username: "" })} />
    </DataProvider>
  )
}

function DataProvider({ username, children }: { username: string; children: ReactNode }) {
  const [info, setInfo] = useState<Info>()
  const [targets, setTargets] = useState<TargetsResponse>({ targets: [], version: "" })
  const [stats, setStats] = useState<Stats>()
  const [freshToken, setFreshToken] = useState<FreshToken>()

  const reloadTargets = useCallback(async () => setTargets(await api.targets()), [])

  useEffect(() => {
    api.info().then(setInfo).catch(() => {})
    reloadTargets().catch(() => {})
    let stopped = false
    const poll = async () => {
      try {
        const s = await api.stats()
        if (!stopped) setStats(s)
      } catch {
        // The next tick retries; a 401 already logs the user out.
      }
    }
    poll()
    const statsTimer = setInterval(poll, 3000)
    // Picks up edits made from another browser or replica.
    const targetsTimer = setInterval(() => reloadTargets().catch(() => {}), 15000)
    return () => {
      stopped = true
      clearInterval(statsTimer)
      clearInterval(targetsTimer)
    }
  }, [reloadTargets])

  return (
    <DataContext.Provider
      value={{
        info,
        targets: targets.targets ?? [],
        version: targets.version,
        stats,
        username,
        applyTargets: setTargets,
        reloadTargets,
        freshToken,
        setFreshToken
      }}
    >
      {children}
    </DataContext.Provider>
  )
}

function Shell({ onLogout }: { onLogout: () => void }) {
  const [page, setPage] = usePage()
  const { info, username } = useRouterData()

  return (
    <div className="flex min-h-full">
      <aside className="sticky top-0 flex h-screen w-64 shrink-0 flex-col border-r border-line bg-ink/40 px-4 py-6">
        <div className="px-2">
          <Brand />
        </div>
        <nav className="mt-10 flex flex-col gap-1">
          {PAGES.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              onClick={() => setPage(id)}
              className={`flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition ${
                page === id
                  ? "bg-lime/10 text-lime shadow-[inset_2px_0_0_var(--color-lime)]"
                  : "text-muted hover:bg-raised hover:text-fg"
              }`}
            >
              <Icon className="size-4" />
              {label}
            </button>
          ))}
        </nav>

        <div className="mt-auto space-y-4">
          <a
            href={WILLOW_URL}
            target="_blank"
            rel="noreferrer"
            className="block rounded-xl border border-line bg-gradient-to-br from-forest/40 to-transparent p-3 text-xs text-muted hover:border-lime/30"
          >
            <img src="/willow-full-logo-white.svg" alt="Willow" className="mb-2 h-4 opacity-90" />
            Governance and analytics for every AI agent in your company.
          </a>
          <div className="flex items-center justify-between px-2 text-xs text-faint">
            <span className="truncate">{username}</span>
            <button
              className="flex items-center gap-1 hover:text-fg"
              onClick={async () => {
                await api.logout().catch(() => {})
                onLogout()
              }}
            >
              <LogOut className="size-3.5" /> Sign out
            </button>
          </div>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <main className="mx-auto w-full max-w-6xl flex-1 px-10 py-10">
          {info?.readOnly && (
            <div className="mb-6 rounded-lg border border-warn/30 bg-warn/10 px-4 py-2.5 text-sm text-warn">
              Read-only mode. Targets and tokens are managed in the router's config, not in this UI.
            </div>
          )}
          {page === "overview" && <Overview />}
          {page === "targets" && <Targets />}
          {page === "sources" && <Sources />}
          {page === "tokens" && <Tokens />}
          {page === "settings" && <Settings />}
        </main>
        <footer className="flex items-center justify-between border-t border-line px-10 py-4 text-xs text-faint">
          <span>
            Open source by{" "}
            <a href={WILLOW_URL} target="_blank" rel="noreferrer" className="text-muted hover:text-lime">
              Willow
            </a>{" "}
            · Apache-2.0 · {info?.version ?? ""}
          </span>
          <span className="flex items-center gap-4">
            <a href={`${GITHUB_URL}#readme`} target="_blank" rel="noreferrer" className="flex items-center gap-1 hover:text-fg">
              <BookOpen className="size-3.5" /> Docs
            </a>
            <a href={GITHUB_URL} target="_blank" rel="noreferrer" className="flex items-center gap-1 hover:text-fg">
              <GithubIcon className="size-3.5" /> GitHub
            </a>
          </span>
        </footer>
      </div>
    </div>
  )
}
