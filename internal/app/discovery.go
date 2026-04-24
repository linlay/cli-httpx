package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

type siteListItem struct {
	Site        string `json:"site"`
	Description string `json:"description"`
	Actions     int    `json:"actions"`
	HasState    bool   `json:"has_state"`
}

type sitesResponse struct {
	Sites []siteListItem `json:"sites"`
}

type loginSummary struct {
	Enabled    bool   `json:"enabled"`
	Type       string `json:"type,omitempty"`
	Path       string `json:"path,omitempty"`
	SecretPath string `json:"secret_path,omitempty"`
}

type siteSummary struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	BaseURL     string        `json:"base_url"`
	Login       *loginSummary `json:"login,omitempty"`
	Actions     int           `json:"actions"`
	State       stateSummary  `json:"state"`
}

type siteResponse struct {
	Site siteSummary `json:"site"`
}

type actionListItem struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Params      []actionInputSpec `json:"params"`
	Extracts    []actionInputSpec `json:"extracts"`
}

type actionsResponse struct {
	Site    string           `json:"site"`
	Actions []actionListItem `json:"actions"`
}

type actionDetail struct {
	Site         string            `json:"site"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	ExpectStatus []int             `json:"expect_status,omitempty"`
	Extractor    *extractorSpec    `json:"extractor,omitempty"`
	Params       []actionInputSpec `json:"params"`
	Extracts     []actionInputSpec `json:"extracts"`
	SaveKeys     []string          `json:"save_keys"`
}

type actionResponse struct {
	Action actionDetail `json:"action"`
}

type stateSummary struct {
	Exists      bool   `json:"exists"`
	Path        string `json:"path"`
	LastLogin   string `json:"last_login,omitempty"`
	SavedValues int    `json:"saved_values"`
	Cookies     int    `json:"cookies"`
}

type stateResponse struct {
	Site  string       `json:"site"`
	State stateSummary `json:"state"`
}

func (rt *Runtime) runListSites(req commandRequest) int {
	sites, err := listConfigSites(req.Options.ConfigDir)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}

	items := make([]siteListItem, 0, len(sites))
	for _, site := range sites {
		cfg, _, err := loadSiteConfig(req.Options.ConfigDir, site)
		if err != nil {
			return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
		}
		state, err := summarizeState(req.Options.StateDir, site)
		if err != nil {
			return rt.writeFailure(req, nil, nil, nil, ExitExecution, "state_error", err.Error())
		}
		items = append(items, siteListItem{
			Site:        site,
			Description: cfg.Description,
			Actions:     len(cfg.Actions),
			HasState:    state.Exists,
		})
	}

	if req.Options.Format == formatJSON {
		if err := writeJSON(rt.stdout, sitesResponse{Sites: items}); err != nil {
			fmt.Fprintf(rt.stderr, "error: %v\n", err)
			return ExitExecution
		}
		return ExitSuccess
	}
	if err := writeSitesText(rt.stdout, items); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return ExitSuccess
}

func (rt *Runtime) runShowSite(req commandRequest) int {
	cfg, _, err := loadSiteConfig(req.Options.ConfigDir, req.Site, req.ConfigProfile)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}
	state, err := summarizeState(req.Options.StateDir, req.Site)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitExecution, "state_error", err.Error())
	}

	summary := siteSummary{
		Name:        req.Site,
		Description: cfg.Description,
		BaseURL:     cfg.BaseURL,
		Login:       summarizeLogin(cfg, req.Options.SecretDir, req.Site),
		Actions:     len(cfg.Actions),
		State:       state,
	}

	if req.Options.Format == formatJSON {
		if err := writeJSON(rt.stdout, siteResponse{Site: summary}); err != nil {
			fmt.Fprintf(rt.stderr, "error: %v\n", err)
			return ExitExecution
		}
		return ExitSuccess
	}
	if err := writeSiteText(rt.stdout, summary); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return ExitSuccess
}

func (rt *Runtime) runListActions(req commandRequest) int {
	cfg, _, err := loadSiteConfig(req.Options.ConfigDir, req.Site, req.ConfigProfile)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}

	names := make([]string, 0, len(cfg.Actions))
	for name := range cfg.Actions {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]actionListItem, 0, len(names))
	for _, name := range names {
		act := cfg.Actions[name]
		merged, err := mergeAction(name, cfg, act, 0)
		if err != nil {
			return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
		}
		items = append(items, actionListItem{
			Name:        name,
			Description: merged.Description,
			Method:      merged.Method,
			Path:        describeActionPath(merged.Path),
			Params:      cloneActionInputSpecs(merged.Params),
			Extracts:    cloneActionInputSpecs(merged.Extracts),
		})
	}

	if req.Options.Format == formatJSON {
		if err := writeJSON(rt.stdout, actionsResponse{Site: req.Site, Actions: items}); err != nil {
			fmt.Fprintf(rt.stderr, "error: %v\n", err)
			return ExitExecution
		}
		return ExitSuccess
	}
	if err := writeActionsText(rt.stdout, req.Site, items); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return ExitSuccess
}

func (rt *Runtime) runShowAction(req commandRequest) int {
	cfg, _, err := loadSiteConfig(req.Options.ConfigDir, req.Site, req.ConfigProfile)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}

	act, err := selectAction(cfg, req.Site, req.Action)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}
	merged, err := mergeAction(req.Action, cfg, act, 0)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}

	saveKeys := make([]string, 0, len(merged.Save))
	for key := range merged.Save {
		saveKeys = append(saveKeys, key)
	}
	sort.Strings(saveKeys)

	detail := actionDetail{
		Site:         req.Site,
		Name:         req.Action,
		Description:  merged.Description,
		Method:       merged.Method,
		Path:         describeActionPath(merged.Path),
		ExpectStatus: append([]int(nil), merged.ExpectStatus...),
		Extractor:    cloneExtractorSpec(merged.Extractor),
		Params:       cloneActionInputSpecs(merged.Params),
		Extracts:     cloneActionInputSpecs(merged.Extracts),
		SaveKeys:     saveKeys,
	}

	if req.Options.Format == formatJSON {
		if err := writeJSON(rt.stdout, actionResponse{Action: detail}); err != nil {
			fmt.Fprintf(rt.stderr, "error: %v\n", err)
			return ExitExecution
		}
		return ExitSuccess
	}
	if err := writeActionText(rt.stdout, detail); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return ExitSuccess
}

func (rt *Runtime) runShowState(req commandRequest) int {
	summary, err := summarizeState(req.Options.StateDir, req.Site)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitExecution, "state_error", err.Error())
	}

	if req.Options.Format == formatJSON {
		if err := writeJSON(rt.stdout, stateResponse{Site: req.Site, State: summary}); err != nil {
			fmt.Fprintf(rt.stderr, "error: %v\n", err)
			return ExitExecution
		}
		return ExitSuccess
	}
	if err := writeStateText(rt.stdout, req.Site, summary); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return ExitSuccess
}

func loadSiteConfig(configDir, site string, profile ...string) (*configFile, string, error) {
	path, err := resolveConfigPath(configDir, site, profile...)
	if err != nil {
		return nil, "", err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

func summarizeState(dir, site string) (stateSummary, error) {
	path := statePath(dir, site)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return stateSummary{Exists: false, Path: path}, nil
		}
		return stateSummary{}, err
	}
	if info.IsDir() {
		return stateSummary{}, fmt.Errorf("%w: state path %q must be a file", ErrExecution, path)
	}

	state, err := loadState(dir, site)
	if err != nil {
		return stateSummary{}, err
	}
	return stateSummary{
		Exists:      true,
		Path:        path,
		LastLogin:   state.LastLogin,
		SavedValues: len(state.Values),
		Cookies:     len(state.Cookies),
	}, nil
}

func writeSitesText(w io.Writer, items []siteListItem) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SITE\tDESCRIPTION\tACTIONS\tSTATE"); err != nil {
		return err
	}
	for _, item := range items {
		state := "no"
		if item.HasState {
			state = "yes"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", item.Site, item.Description, item.Actions, state); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeSiteText(w io.Writer, summary siteSummary) error {
	loginMode := "none"
	loginPath := "-"
	loginSecretPath := "-"
	if summary.Login != nil && summary.Login.Enabled {
		loginMode = summary.Login.Type
		loginPath = summary.Login.Path
		loginSecretPath = summary.Login.SecretPath
	}
	_, err := fmt.Fprintf(w,
		"site: %s\ndescription: %s\nbase_url: %s\nlogin: %s\nlogin_path: %s\nlogin_secret_path: %s\nactions: %d\nstate_exists: %t\nstate_path: %s\nlast_login: %s\nsaved_values: %d\ncookies: %d\n",
		summary.Name,
		summary.Description,
		summary.BaseURL,
		loginMode,
		loginPath,
		loginSecretPath,
		summary.Actions,
		summary.State.Exists,
		summary.State.Path,
		emptyText(summary.State.LastLogin),
		summary.State.SavedValues,
		summary.State.Cookies,
	)
	return err
}

func writeActionsText(w io.Writer, site string, actions []actionListItem) error {
	if _, err := fmt.Fprintf(w, "site: %s\n", site); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ACTION\tDESCRIPTION"); err != nil {
		return err
	}
	for _, action := range actions {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", action.Name, action.Description); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeActionText(w io.Writer, detail actionDetail) error {
	if _, err := fmt.Fprintf(w, "Usage:\n  httpx run %s %s [flags]\n", detail.Site, detail.Name); err != nil {
		return err
	}
	if err := writeParagraphSection(w, "Description", detail.Description); err != nil {
		return err
	}
	if err := writeActionFlagsSection(w, len(detail.Params) > 0, len(detail.Extracts) > 0); err != nil {
		return err
	}
	if err := writeInputSpecsTable(w, "Params fields", detail.Params); err != nil {
		return err
	}
	if err := writeInputSpecsTable(w, "Extracts fields", detail.Extracts); err != nil {
		return err
	}
	return writeExamplesSection(w, buildActionExamples(detail))
}

func writeParagraphSection(w io.Writer, title, body string) error {
	if _, err := fmt.Fprintf(w, "\n%s:\n", title); err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func writeActionFlagsSection(w io.Writer, hasParams, hasExtracts bool) error {
	if _, err := fmt.Fprint(w, "\nFlags:\n"); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if hasParams {
		if _, err := fmt.Fprintln(tw, "--param key=value\tRequest parameter in key=value form; repeatable"); err != nil {
			return err
		}
	}
	if hasExtracts {
		if _, err := fmt.Fprintln(tw, "--extract <json-object>\tExtractor input as a JSON object"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(tw, "-h, --help\thelp for this action"); err != nil {
		return err
	}
	return tw.Flush()
}

func writeInputSpecsTable(w io.Writer, title string, specs []actionInputSpec) error {
	if len(specs) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s:\n", title); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "name\ttype\trequired\tdefault\tdescription\texample"); err != nil {
		return err
	}
	for _, spec := range specs {
		required := "no"
		if spec.Required {
			required = "yes"
		}
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t-\t%s\t%s\n",
			emptyText(spec.Name),
			emptyText(spec.Type),
			required,
			emptyText(spec.Description),
			describeExample(spec.Example),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeExamplesSection(w io.Writer, examples []string) error {
	if len(examples) == 0 {
		return nil
	}
	if _, err := fmt.Fprint(w, "\nExamples:\n"); err != nil {
		return err
	}
	for _, example := range examples {
		if _, err := fmt.Fprintf(w, "  %s\n", example); err != nil {
			return err
		}
	}
	return nil
}

func buildActionExamples(detail actionDetail) []string {
	base := fmt.Sprintf("httpx run %s %s", detail.Site, detail.Name)
	paramArgs := buildParamExampleArgs(detail.Params)
	extractArg := buildExtractExampleArg(detail.Extracts)

	candidates := []string{base}
	if len(paramArgs) > 0 {
		candidates = append(candidates, base+" "+strings.Join(paramArgs, " "))
	}
	if extractArg != "" {
		candidates = append(candidates, base+" "+extractArg)
	}
	if len(paramArgs) > 0 && extractArg != "" {
		candidates = append(candidates, base+" "+strings.Join(paramArgs, " ")+" "+extractArg)
	}

	seen := make(map[string]struct{}, len(candidates))
	examples := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		examples = append(examples, candidate)
	}
	return examples
}

func buildParamExampleArgs(specs []actionInputSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	args := make([]string, 0, len(specs))
	for _, spec := range specs {
		if !spec.Required && spec.Example == nil {
			continue
		}
		value := renderParamExampleValue(spec)
		arg := spec.Name + "=" + value
		if needsShellQuoting(arg) {
			arg = shellSingleQuote(arg)
		}
		args = append(args, "--param "+arg)
	}
	if len(args) == 0 {
		fallback := specs[0].Name + "=<" + specs[0].Name + ">"
		if needsShellQuoting(fallback) {
			fallback = shellSingleQuote(fallback)
		}
		args = append(args, "--param "+fallback)
	}
	return args
}

func buildExtractExampleArg(specs []actionInputSpec) string {
	if len(specs) == 0 {
		return ""
	}
	payload := make(map[string]any)
	for _, spec := range specs {
		if spec.Example != nil {
			payload[spec.Name] = spec.Example
			continue
		}
		if spec.Required {
			payload[spec.Name] = "<" + spec.Name + ">"
		}
	}
	if len(payload) == 0 {
		payload[specs[0].Name] = "<" + specs[0].Name + ">"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "--extract '{...}'"
	}
	return "--extract " + shellSingleQuote(string(data))
}

func renderParamExampleValue(spec actionInputSpec) string {
	if spec.Example == nil {
		return "<" + spec.Name + ">"
	}
	switch value := spec.Example.(type) {
	case string:
		if value == "" {
			return "<" + spec.Name + ">"
		}
		return value
	case bool:
		if value {
			return "true"
		}
		return "false"
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", value)
	default:
		return "<" + spec.Name + ">"
	}
}

func needsShellQuoting(value string) bool {
	return strings.ContainsAny(value, " \t\n'\"\\$`!&;|<>()[]{}")
}

func shellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func describeExample(value any) string {
	if value == nil {
		return "-"
	}
	rendered, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(rendered)
}

func writeStateText(w io.Writer, site string, state stateSummary) error {
	_, err := fmt.Fprintf(w,
		"site: %s\nexists: %t\npath: %s\nlast_login: %s\nsaved_values: %d\ncookies: %d\n",
		site,
		state.Exists,
		state.Path,
		emptyText(state.LastLogin),
		state.SavedValues,
		state.Cookies,
	)
	return err
}

func emptyText(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func describeActionPath(path any) string {
	switch value := path.(type) {
	case string:
		return value
	default:
		rendered, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
		return string(rendered)
	}
}

func summarizeLogin(cfg *configFile, secretDir, site string) *loginSummary {
	if cfg.Login == nil {
		return nil
	}
	return &loginSummary{
		Enabled:    true,
		Type:       "basic",
		Path:       cfg.Login.Path,
		SecretPath: secretPath(secretDir, site),
	}
}
