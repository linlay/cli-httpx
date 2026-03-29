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

type siteSummary struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	BaseURL     string       `json:"base_url"`
	LoginAction string       `json:"login_action,omitempty"`
	Actions     int          `json:"actions"`
	State       stateSummary `json:"state"`
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
	cfg, _, err := loadSiteConfig(req.Options.ConfigDir, req.Site)
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
		LoginAction: cfg.LoginAction,
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
	cfg, _, err := loadSiteConfig(req.Options.ConfigDir, req.Site)
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
			Path:        merged.Path,
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
	cfg, _, err := loadSiteConfig(req.Options.ConfigDir, req.Site)
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
		Path:         merged.Path,
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

func loadSiteConfig(configDir, site string) (*configFile, string, error) {
	path, err := resolveConfigPath(configDir, site)
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
	_, err := fmt.Fprintf(w,
		"site: %s\ndescription: %s\nbase_url: %s\nlogin_action: %s\nactions: %d\nstate_exists: %t\nstate_path: %s\nlast_login: %s\nsaved_values: %d\ncookies: %d\n",
		summary.Name,
		summary.Description,
		summary.BaseURL,
		summary.LoginAction,
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
	for i, action := range actions {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w,
			"\naction: %s\ndescription: %s\nmethod: %s\npath: %s\n",
			action.Name,
			action.Description,
			action.Method,
			action.Path,
		); err != nil {
			return err
		}
		if err := writeInputSpecsText(w, "params", action.Params); err != nil {
			return err
		}
		if err := writeInputSpecsText(w, "extracts", action.Extracts); err != nil {
			return err
		}
	}
	return nil
}

func writeActionText(w io.Writer, detail actionDetail) error {
	if _, err := fmt.Fprintf(w,
		"site: %s\naction: %s\ndescription: %s\nmethod: %s\npath: %s\nextractor: %s\nexpect_status: %s\nsave_keys: %s\n",
		detail.Site,
		detail.Name,
		detail.Description,
		detail.Method,
		detail.Path,
		describeExtractor(detail.Extractor),
		describeStatuses(detail.ExpectStatus),
		describeSaveKeys(detail.SaveKeys),
	); err != nil {
		return err
	}
	if err := writeInputSpecsText(w, "params", detail.Params); err != nil {
		return err
	}
	return writeInputSpecsText(w, "extracts", detail.Extracts)
}

func writeInputSpecsText(w io.Writer, label string, specs []actionInputSpec) error {
	if len(specs) == 0 {
		_, err := fmt.Fprintf(w, "%s: []\n", label)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s:\n", label); err != nil {
		return err
	}
	for _, spec := range specs {
		if _, err := fmt.Fprintf(w,
			"  - name: %s\n    type: %s\n    required: %t\n    description: %s\n    example: %s\n",
			emptyText(spec.Name),
			emptyText(spec.Type),
			spec.Required,
			emptyText(spec.Description),
			describeExample(spec.Example),
		); err != nil {
			return err
		}
	}
	return nil
}

func describeExtractor(spec *extractorSpec) string {
	if spec == nil {
		return "-"
	}
	return spec.Type
}

func describeStatuses(statuses []int) string {
	if len(statuses) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%d", status))
	}
	return strings.Join(parts, ",")
}

func describeSaveKeys(keys []string) string {
	if len(keys) == 0 {
		return "-"
	}
	return strings.Join(keys, ",")
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
