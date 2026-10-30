package filter

type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Rules       []Rule `json:"rules"`
}

// promptEvents are the log events in which agents carry the user's prompt.
var promptEvents = []string{"*user_prompt", "*.user_prompt", "gen_ai.user.message"}

// contentKeys are attributes that may hold prompt, completion or tool content.
var contentKeys = []string{
	"prompt", "user.prompt", "gen_ai.prompt*", "gen_ai.completion*",
	"gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions",
	"tool_input", "tool_parameters", "tool.arguments", "gen_ai.tool.call.arguments", "gen_ai.tool.call.result",
}

var identityKeys = []string{"user.email", "user.id", "user.account_uuid", "enduser.id", "host.name"}

var Presets = []Preset{
	{
		ID:          "privacy",
		Name:        "Privacy",
		Description: "Drop prompt events and content, hash user identity, redact secrets.",
		Rules: []Rule{
			{Type: DropLogEvents, Names: promptEvents},
			{Type: DropAttributes, Keys: contentKeys},
			{Type: HashAttributes, Keys: identityKeys},
			{Type: Redact, Builtins: true},
		},
	},
	{
		ID:          "anonymous",
		Name:        "Anonymous usage",
		Description: "Like Privacy, but drops user identity instead of hashing it.",
		Rules: []Rule{
			{Type: DropLogEvents, Names: promptEvents},
			{Type: DropAttributes, Keys: append(append([]string{}, contentKeys...), identityKeys...)},
			{Type: Redact, Builtins: true},
		},
	},
	{
		ID:          "secrets",
		Name:        "Redact secrets",
		Description: "Replace API keys, tokens, JWTs and private keys with [REDACTED].",
		Rules:       []Rule{{Type: Redact, Builtins: true}},
	},
}
