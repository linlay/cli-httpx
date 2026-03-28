package app

import (
	"fmt"
	"io"
	"os"
	"sort"
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

type actionSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type actionsResponse struct {
	Site    string          `json:"site"`
	Actions []actionSummary `json:"actions"`
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

	items := make([]actionSummary, 0, len(names))
	for _, name := range names {
		act := cfg.Actions[name]
		items = append(items, actionSummary{
			Name:        name,
			Description: act.Description,
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

func writeActionsText(w io.Writer, site string, actions []actionSummary) error {
	if _, err := fmt.Fprintf(w, "site: %s\n", site); err != nil {
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
