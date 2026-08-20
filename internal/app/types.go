package app

import "time"

type outputFormat string

const (
	formatText outputFormat = "text"
	formatJSON outputFormat = "json"
)

type storageScope string

const (
	scopeGlobal storageScope = "global"
	scopeChat   storageScope = "chat"
)

type commandKind string

const (
	commandRun     commandKind = "run"
	commandInspect commandKind = "inspect"
	commandLogin   commandKind = "login"
	commandSites   commandKind = "sites"
	commandSite    commandKind = "site"
	commandAction  commandKind = "action"
	commandActions commandKind = "actions"
	commandState   commandKind = "state"
)

type globalOptions struct {
	ConfigDir      string
	ConfigExplicit bool
	Format         outputFormat
	SecretDir      string
	Timeout        time.Duration
	StateDir       string
	ChatDir        string
	ChatDirSet     bool
	Inspect        bool
	Reveal         bool
	Params         map[string]any
	ExtractInput   map[string]any
}

type commandRequest struct {
	Command commandKind
	Site    string
	Action  string
	Options globalOptions
}

type envelope struct {
	OK           bool                `json:"ok"`
	Site         string              `json:"site,omitempty"`
	Action       string              `json:"action,omitempty"`
	Status       int                 `json:"status,omitempty"`
	DurationMS   int64               `json:"duration_ms,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         any                 `json:"body,omitempty"`
	Download     *downloadResult     `json:"download,omitempty"`
	StateUpdated []string            `json:"state_updated,omitempty"`
	Error        *errorEnvelope      `json:"error,omitempty"`
}

type downloadResult struct {
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type,omitempty"`
}

type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
