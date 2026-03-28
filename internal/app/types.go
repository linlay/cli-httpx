package app

import "time"

type outputFormat string

const (
	formatText outputFormat = "text"
	formatJSON outputFormat = "json"
	formatBody outputFormat = "body"
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
	ConfigDir    string
	Format       outputFormat
	Timeout      time.Duration
	StateDir     string
	Inspect      bool
	Reveal       bool
	Params       map[string]string
	ExtractInput map[string]any
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
	Extract      any                 `json:"extract,omitempty"`
	StateUpdated []string            `json:"state_updated,omitempty"`
	Error        *errorEnvelope      `json:"error,omitempty"`
}

type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
