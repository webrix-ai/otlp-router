import type { Signal } from "./api"

export interface AgentDef {
  slug: string
  name: string
  vendor: string
  signals: Signal[]
  where: string
  language: "bash" | "toml" | "json"
  snippet: (url: string, token: string) => string
  note?: string
}

const authHeader = (token: string) => (token ? `Authorization=Bearer ${token}` : "")

export const AGENTS: AgentDef[] = [
  {
    slug: "claude-code",
    name: "Claude Code",
    vendor: "Anthropic",
    signals: ["metrics", "logs"],
    where: "Shell profile, or the env block of managed-settings.json",
    language: "bash",
    snippet: (url, token) =>
      [
        "export CLAUDE_CODE_ENABLE_TELEMETRY=1",
        "export OTEL_METRICS_EXPORTER=otlp",
        "export OTEL_LOGS_EXPORTER=otlp",
        "export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
        `export OTEL_EXPORTER_OTLP_ENDPOINT=${url}`,
        token ? `export OTEL_EXPORTER_OTLP_HEADERS="${authHeader(token)}"` : null
      ]
        .filter(Boolean)
        .join("\n"),
    note: "Prompts are only exported when OTEL_LOG_USER_PROMPTS=1."
  },
  {
    slug: "codex",
    name: "Codex",
    vendor: "OpenAI",
    signals: ["logs", "traces"],
    where: "~/.codex/config.toml",
    language: "toml",
    snippet: (url, token) =>
      [
        "[otel]",
        'environment = "prod"',
        "log_user_prompt = false",
        `exporter = { otlp-http = { endpoint = "${url}/v1/logs", protocol = "binary"${
          token ? `, headers = { "Authorization" = "Bearer ${token}" }` : ""
        } } }`
      ].join("\n")
  },
  {
    slug: "gemini-cli",
    name: "Gemini CLI",
    vendor: "Google",
    signals: ["traces", "metrics", "logs"],
    where: "~/.gemini/settings.json",
    language: "json",
    snippet: (url) =>
      JSON.stringify(
        {
          telemetry: { enabled: true, target: "local", otlpEndpoint: url, otlpProtocol: "http", logPrompts: false }
        },
        null,
        2
      ),
    note: "Set OTEL_EXPORTER_OTLP_HEADERS for the ingest token when it is enabled."
  },
  {
    slug: "github-copilot",
    name: "GitHub Copilot",
    vendor: "GitHub",
    signals: ["traces", "metrics", "logs"],
    where: "Environment of VS Code or the Copilot CLI",
    language: "bash",
    snippet: (url, token) =>
      [
        "export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
        `export OTEL_EXPORTER_OTLP_ENDPOINT=${url}`,
        token ? `export OTEL_EXPORTER_OTLP_HEADERS="${authHeader(token)}"` : null
      ]
        .filter(Boolean)
        .join("\n"),
    note: "Enable Copilot's OpenTelemetry export in its settings; it then uses the standard OTLP variables."
  },
  {
    slug: "otel-sdk",
    name: "Agent SDKs",
    vendor: "OpenAI Agents SDK, Claude Agent SDK, LangChain and any OTel SDK",
    signals: ["traces", "metrics", "logs"],
    where: "The agent process environment",
    language: "bash",
    snippet: (url, token) =>
      [
        "export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
        `export OTEL_EXPORTER_OTLP_ENDPOINT=${url}`,
        token ? `export OTEL_EXPORTER_OTLP_HEADERS="${authHeader(token)}"` : null,
        "export OTEL_SERVICE_NAME=my-agent"
      ]
        .filter(Boolean)
        .join("\n")
  }
]

export const KNOWN_AGENT_SLUGS = [
  "claude-code",
  "claude-cowork",
  "codex",
  "gemini-cli",
  "qwen-code",
  "github-copilot",
  "cursor",
  "pi"
]

const NAMES: Record<string, string> = {
  "claude-code": "Claude Code",
  "claude-cowork": "Claude Cowork",
  codex: "Codex",
  "gemini-cli": "Gemini CLI",
  "qwen-code": "Qwen Code",
  "github-copilot": "GitHub Copilot",
  cursor: "Cursor",
  pi: "Pi",
  unknown: "Unknown"
}

export function agentName(slug: string): string {
  return NAMES[slug] ?? slug
}
