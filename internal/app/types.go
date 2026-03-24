package app

import "time"

type outputFormat string

const (
	formatJSON outputFormat = "json"
	formatBody outputFormat = "body"
)

type globalOptions struct {
	ConfigPath string
	Format     outputFormat
	Timeout    time.Duration
	StateDir   string
	Reveal     bool
}

type commandKind string

const (
	commandRun     commandKind = "run"
	commandLogin   commandKind = "login"
	commandInspect commandKind = "inspect"
)

type commandRequest struct {
	Kind    commandKind
	Profile string
	Action  string
	Options globalOptions
}

type envelope struct {
	OK           bool                `json:"ok"`
	Profile      string              `json:"profile,omitempty"`
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
