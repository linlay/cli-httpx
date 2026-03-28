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
	Description string            `toml:"description"`
	BaseURL     string            `toml:"base_url"`
	LoginAction string            `toml:"login_action"`
	Proxy       any               `toml:"proxy"`
	Timeout     durationValue     `toml:"timeout"`
	Retries     int               `toml:"retries"`
	Headers     map[string]any    `toml:"headers"`
	Cookies     map[string]any    `toml:"cookies"`
	Query       map[string]any    `toml:"query"`
	Actions     map[string]action `toml:"actions"`
	Version     int               `toml:"version"`
}

type action struct {
	Description    string            `toml:"description"`
	Method         string            `toml:"method"`
	Path           string            `toml:"path"`
	Proxy          any               `toml:"proxy"`
	Timeout        *durationValue    `toml:"timeout"`
	Retries        *int              `toml:"retries"`
	Headers        map[string]any    `toml:"headers"`
	Cookies        map[string]any    `toml:"cookies"`
	Query          map[string]any    `toml:"query"`
	Body           any               `toml:"body"`
	Form           map[string]any    `toml:"form"`
	ExpectStatus   any               `toml:"expect_status"`
	ExtractType    string            `toml:"extract_type"`
	ExtractExpr    string            `toml:"extract_expr"`
	ExtractPattern string            `toml:"extract_pattern"`
	ExtractGroup   *int              `toml:"extract_group"`
	ExtractAll     *bool             `toml:"extract_all"`
	Params         []actionInputSpec `toml:"params"`
	Extracts       []actionInputSpec `toml:"extracts"`
	Save           map[string]string `toml:"save"`
}

type mergedAction struct {
	Name         string
	Description  string
	Method       string
	Path         string
	Proxy        any
	Timeout      time.Duration
	Retries      int
	Headers      map[string]any
	Cookies      map[string]any
	Query        map[string]any
	Body         any
	Form         map[string]any
	ExpectStatus []int
	Extractor    *extractorSpec
	Params       []actionInputSpec
	Extracts     []actionInputSpec
	Save         map[string]string
}

type actionInputSpec struct {
	Name        string   `toml:"name" json:"name"`
	Type        string   `toml:"type" json:"type,omitempty"`
	Required    bool     `toml:"required" json:"required"`
	Description string   `toml:"description" json:"description,omitempty"`
	Example     any      `toml:"example" json:"example,omitempty"`
	Enum        []string `toml:"enum" json:"enum,omitempty"`
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
	if strings.TrimSpace(cfg.Description) == "" {
		return fmt.Errorf("%w: description is required", ErrConfig)
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("%w: base_url is required", ErrConfig)
	}
	if len(cfg.Actions) == 0 {
		return fmt.Errorf("%w: actions is required", ErrConfig)
	}
	for actionName, act := range cfg.Actions {
		if strings.TrimSpace(act.Description) == "" {
			return fmt.Errorf("%w: actions.%s.description is required", ErrConfig, actionName)
		}
		if strings.TrimSpace(act.Path) == "" {
			return fmt.Errorf("%w: actions.%s.path is required", ErrConfig, actionName)
		}
		if act.Body != nil && len(act.Form) > 0 {
			return fmt.Errorf("%w: actions.%s cannot set both body and form", ErrConfig, actionName)
		}
		if _, err := normalizeExpectStatus(act.ExpectStatus); err != nil {
			return fmt.Errorf("%w: actions.%s.expect_status: %v", ErrConfig, actionName, err)
		}
		extractor, err := extractorFromAction(actionName, act)
		if err != nil {
			return err
		}
		if _, err := compileExtractor(actionName, extractor, nil); err != nil {
			return err
		}
		if err := validateActionInputSpecs("actions."+actionName+".params", act.Params); err != nil {
			return err
		}
		if err := validateActionInputSpecs("actions."+actionName+".extracts", act.Extracts); err != nil {
			return err
		}
	}
	if cfg.LoginAction != "" {
		if _, ok := cfg.Actions[cfg.LoginAction]; !ok {
			return fmt.Errorf("%w: login_action references missing action %q", ErrConfig, cfg.LoginAction)
		}
	}
	return nil
}

func selectAction(cfg *configFile, siteName, actionName string) (action, error) {
	act, ok := cfg.Actions[actionName]
	if !ok {
		return action{}, fmt.Errorf("%w: unknown action %q for site %q", ErrConfig, actionName, siteName)
	}
	return act, nil
}

func mergeAction(actionName string, cfg *configFile, act action, timeoutOverride time.Duration) (mergedAction, error) {
	expectStatus, err := normalizeExpectStatus(act.ExpectStatus)
	if err != nil {
		return mergedAction{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}

	timeout := cfg.Timeout.Duration
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	if act.Timeout != nil && act.Timeout.Duration > 0 {
		timeout = act.Timeout.Duration
	}
	if timeoutOverride > 0 {
		timeout = timeoutOverride
	}

	retries := cfg.Retries
	if act.Retries != nil {
		retries = *act.Retries
	}
	if retries < 0 {
		return mergedAction{}, fmt.Errorf("%w: retries cannot be negative", ErrConfig)
	}
	extractor, err := extractorFromAction(actionName, act)
	if err != nil {
		return mergedAction{}, err
	}

	method := strings.ToUpper(strings.TrimSpace(act.Method))
	if method == "" {
		if act.Body != nil || len(act.Form) > 0 {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	proxy := cfg.Proxy
	if act.Proxy != nil {
		proxy = act.Proxy
	}

	return mergedAction{
		Name:         actionName,
		Description:  act.Description,
		Method:       method,
		Path:         act.Path,
		Proxy:        proxy,
		Timeout:      timeout,
		Retries:      retries,
		Headers:      mergeMap(cfg.Headers, act.Headers),
		Cookies:      mergeMap(cfg.Cookies, act.Cookies),
		Query:        mergeMap(cfg.Query, act.Query),
		Body:         act.Body,
		Form:         copyMap(act.Form),
		ExpectStatus: expectStatus,
		Extractor:    extractor,
		Params:       cloneActionInputSpecs(act.Params),
		Extracts:     cloneActionInputSpecs(act.Extracts),
		Save:         copyStringMap(act.Save),
	}, nil
}

func validateActionInputSpecs(prefix string, specs []actionInputSpec) error {
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			return fmt.Errorf("%w: %s.name is required", ErrConfig, prefix)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%w: %s contains duplicate name %q", ErrConfig, prefix, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func cloneActionInputSpecs(specs []actionInputSpec) []actionInputSpec {
	if len(specs) == 0 {
		return []actionInputSpec{}
	}
	out := make([]actionInputSpec, len(specs))
	for i, spec := range specs {
		out[i] = actionInputSpec{
			Name:        spec.Name,
			Type:        spec.Type,
			Required:    spec.Required,
			Description: spec.Description,
			Example:     cloneJSONValue(spec.Example),
			Enum:        append([]string(nil), spec.Enum...),
		}
	}
	return out
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
