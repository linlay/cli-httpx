package app

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const defaultRequestTimeout = 30 * time.Second

type durationValue struct {
	time.Duration
}

func (d *durationValue) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = value
	return nil
}

type configFile struct {
	Version  int                `toml:"version"`
	Profiles map[string]profile `toml:"profiles"`
}

type profile struct {
	BaseURL     string            `toml:"base_url"`
	LoginAction string            `toml:"login_action"`
	Timeout     durationValue     `toml:"timeout"`
	Retries     int               `toml:"retries"`
	Headers     map[string]any    `toml:"headers"`
	Cookies     map[string]any    `toml:"cookies"`
	Query       map[string]any    `toml:"query"`
	Actions     map[string]action `toml:"actions"`
}

type action struct {
	Method       string            `toml:"method"`
	Path         string            `toml:"path"`
	Timeout      *durationValue    `toml:"timeout"`
	Retries      *int              `toml:"retries"`
	Headers      map[string]any    `toml:"headers"`
	Cookies      map[string]any    `toml:"cookies"`
	Query        map[string]any    `toml:"query"`
	Body         any               `toml:"body"`
	Form         map[string]any    `toml:"form"`
	ExpectStatus any               `toml:"expect_status"`
	Extract      string            `toml:"extract"`
	Save         map[string]string `toml:"save"`
}

type mergedAction struct {
	Name         string
	Method       string
	Path         string
	Timeout      time.Duration
	Retries      int
	Headers      map[string]any
	Cookies      map[string]any
	Query        map[string]any
	Body         any
	Form         map[string]any
	ExpectStatus []int
	Extract      string
	Save         map[string]string
}

func loadConfig(path string) (*configFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read config: %v", ErrConfig, err)
	}

	var cfg configFile
	dec := toml.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: decode config: %v", ErrConfig, err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateConfig(cfg *configFile) error {
	if cfg.Version != 1 {
		return fmt.Errorf("%w: unsupported config version %d", ErrConfig, cfg.Version)
	}
	if len(cfg.Profiles) == 0 {
		return fmt.Errorf("%w: no profiles configured", ErrConfig)
	}
	for name, prof := range cfg.Profiles {
		if strings.TrimSpace(prof.BaseURL) == "" {
			return fmt.Errorf("%w: profiles.%s.base_url is required", ErrConfig, name)
		}
		if len(prof.Actions) == 0 {
			return fmt.Errorf("%w: profiles.%s.actions is required", ErrConfig, name)
		}
		for actionName, act := range prof.Actions {
			if strings.TrimSpace(act.Path) == "" {
				return fmt.Errorf("%w: profiles.%s.actions.%s.path is required", ErrConfig, name, actionName)
			}
			if act.Body != nil && len(act.Form) > 0 {
				return fmt.Errorf("%w: profiles.%s.actions.%s cannot set both body and form", ErrConfig, name, actionName)
			}
			if _, err := normalizeExpectStatus(act.ExpectStatus); err != nil {
				return fmt.Errorf("%w: profiles.%s.actions.%s.expect_status: %v", ErrConfig, name, actionName, err)
			}
		}
		if prof.LoginAction != "" {
			if _, ok := prof.Actions[prof.LoginAction]; !ok {
				return fmt.Errorf("%w: profiles.%s.login_action references missing action %q", ErrConfig, name, prof.LoginAction)
			}
		}
	}
	return nil
}

func selectAction(cfg *configFile, profileName, actionName string) (profile, action, error) {
	prof, ok := cfg.Profiles[profileName]
	if !ok {
		return profile{}, action{}, fmt.Errorf("%w: unknown profile %q", ErrConfig, profileName)
	}
	act, ok := prof.Actions[actionName]
	if !ok {
		return profile{}, action{}, fmt.Errorf("%w: unknown action %q for profile %q", ErrConfig, actionName, profileName)
	}
	return prof, act, nil
}

func mergeAction(actionName string, prof profile, act action, timeoutOverride time.Duration) (mergedAction, error) {
	expectStatus, err := normalizeExpectStatus(act.ExpectStatus)
	if err != nil {
		return mergedAction{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}

	timeout := prof.Timeout.Duration
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	if act.Timeout != nil && act.Timeout.Duration > 0 {
		timeout = act.Timeout.Duration
	}
	if timeoutOverride > 0 {
		timeout = timeoutOverride
	}

	retries := prof.Retries
	if act.Retries != nil {
		retries = *act.Retries
	}
	if retries < 0 {
		return mergedAction{}, fmt.Errorf("%w: retries cannot be negative", ErrConfig)
	}

	method := strings.ToUpper(strings.TrimSpace(act.Method))
	if method == "" {
		if act.Body != nil || len(act.Form) > 0 {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	return mergedAction{
		Name:         actionName,
		Method:       method,
		Path:         act.Path,
		Timeout:      timeout,
		Retries:      retries,
		Headers:      mergeMap(prof.Headers, act.Headers),
		Cookies:      mergeMap(prof.Cookies, act.Cookies),
		Query:        mergeMap(prof.Query, act.Query),
		Body:         act.Body,
		Form:         copyMap(act.Form),
		ExpectStatus: expectStatus,
		Extract:      act.Extract,
		Save:         copyStringMap(act.Save),
	}, nil
}

func normalizeExpectStatus(value any) ([]int, error) {
	if value == nil {
		return nil, nil
	}

	switch typed := value.(type) {
	case int64:
		return []int{int(typed)}, nil
	case int32:
		return []int{int(typed)}, nil
	case int:
		return []int{typed}, nil
	case []any:
		statuses := make([]int, 0, len(typed))
		for _, item := range typed {
			status, ok := integerValue(item)
			if !ok {
				return nil, fmt.Errorf("expected integer or integer array")
			}
			statuses = append(statuses, status)
		}
		return statuses, nil
	default:
		return nil, fmt.Errorf("expected integer or integer array")
	}
}

func integerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case int32:
		return int(typed), true
	default:
		return 0, false
	}
}

func mergeMap(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func copyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func copyStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
