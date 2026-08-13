package app

import (
	"bytes"
	"fmt"
	"net/http"
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
	StateScope  string            `toml:"state_scope"`
	Login       *loginConfig      `toml:"login"`
	Proxy       any               `toml:"proxy"`
	Timeout     durationValue     `toml:"timeout"`
	Retries     int               `toml:"retries"`
	Headers     map[string]any    `toml:"headers"`
	Cookies     map[string]any    `toml:"cookies"`
	Query       map[string]any    `toml:"query"`
	Actions     map[string]action `toml:"actions"`
	Version     int               `toml:"version"`
}

type loginConfig struct {
	Method         string            `toml:"method"`
	Path           string            `toml:"path"`
	SecretScope    string            `toml:"secret_scope"`
	BodyFormat     string            `toml:"body_format"`
	UsernameField  string            `toml:"username_field"`
	PasswordField  string            `toml:"password_field"`
	BasicAuth      bool              `toml:"basic_auth"`
	Headers        map[string]any    `toml:"headers"`
	ExpectStatus   any               `toml:"expect_status"`
	ExtractType    string            `toml:"extract_type"`
	ExtractExpr    string            `toml:"extract_expr"`
	ExtractPattern string            `toml:"extract_pattern"`
	ExtractGroup   *int              `toml:"extract_group"`
	ExtractAll     *bool             `toml:"extract_all"`
	Save           map[string]string `toml:"save"`
}

type action struct {
	Description    string            `toml:"description"`
	Method         string            `toml:"method"`
	Path           any               `toml:"path"`
	Proxy          any               `toml:"proxy"`
	Timeout        *durationValue    `toml:"timeout"`
	Retries        *int              `toml:"retries"`
	Headers        map[string]any    `toml:"headers"`
	Cookies        map[string]any    `toml:"cookies"`
	Query          map[string]any    `toml:"query"`
	Body           any               `toml:"body"`
	Form           map[string]any    `toml:"form"`
	Multipart      []multipartPart   `toml:"multipart"`
	Download       *downloadConfig   `toml:"download"`
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

type multipartPart struct {
	Name        string `toml:"name"`
	Value       any    `toml:"value"`
	File        any    `toml:"file"`
	Filename    any    `toml:"filename"`
	ContentType string `toml:"content_type"`
	MaxBytes    int64  `toml:"max_bytes"`
}

type downloadConfig struct {
	Path      any   `toml:"path"`
	Overwrite any   `toml:"overwrite"`
	MaxBytes  int64 `toml:"max_bytes"`
}

type mergedAction struct {
	Name         string
	Description  string
	Method       string
	Path         any
	Proxy        any
	Timeout      time.Duration
	Retries      int
	Headers      map[string]any
	Cookies      map[string]any
	Query        map[string]any
	Body         any
	Form         map[string]any
	Multipart    []multipartPart
	Download     *downloadConfig
	ExpectStatus []int
	Extractor    *extractorSpec
	Params       []actionInputSpec
	Extracts     []actionInputSpec
	Save         map[string]string
}

type mergedLogin struct {
	Method       string
	Path         string
	SecretScope  storageScope
	BodyFormat   string
	UsernameKey  string
	PasswordKey  string
	BasicAuth    bool
	Headers      map[string]any
	Timeout      time.Duration
	Retries      int
	ExpectStatus []int
	Extractor    *extractorSpec
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
		return nil, fmt.Errorf("%w: read config %q: %v", ErrConfig, path, err)
	}

	var cfg configFile
	dec := toml.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: decode config %q: %v", ErrConfig, path, err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("%w: validate config %q: %v", ErrConfig, path, err)
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
	if _, err := normalizeStorageScope(cfg.StateScope); err != nil {
		return fmt.Errorf("%w: state_scope: %v", ErrConfig, err)
	}
	if len(cfg.Actions) == 0 && cfg.Login == nil {
		return fmt.Errorf("%w: actions or login is required", ErrConfig)
	}
	for actionName, act := range cfg.Actions {
		if strings.TrimSpace(act.Description) == "" {
			return fmt.Errorf("%w: actions.%s.description is required", ErrConfig, actionName)
		}
		if err := validateActionPath("actions."+actionName+".path", act.Path); err != nil {
			return err
		}
		bodyKinds := 0
		if act.Body != nil {
			bodyKinds++
			if _, ok := act.Body.(map[string]any); !ok {
				return fmt.Errorf("%w: actions.%s.body must be an object", ErrConfig, actionName)
			}
		}
		if len(act.Form) > 0 {
			bodyKinds++
		}
		if act.Multipart != nil {
			bodyKinds++
		}
		if bodyKinds > 1 {
			return fmt.Errorf("%w: actions.%s body, form, and multipart are mutually exclusive", ErrConfig, actionName)
		}
		if act.Multipart != nil {
			if len(act.Multipart) == 0 {
				return fmt.Errorf("%w: actions.%s.multipart must contain at least one part", ErrConfig, actionName)
			}
			for index, part := range act.Multipart {
				prefix := fmt.Sprintf("actions.%s.multipart[%d]", actionName, index)
				if err := validateMultipartPart(prefix, part); err != nil {
					return err
				}
			}
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
		if act.Download != nil {
			if err := validateDownloadConfig("actions."+actionName+".download", act.Download); err != nil {
				return err
			}
			if extractor != nil {
				return fmt.Errorf("%w: actions.%s.download cannot be combined with a response extractor", ErrConfig, actionName)
			}
			if len(act.Save) > 0 {
				return fmt.Errorf("%w: actions.%s.download cannot be combined with save", ErrConfig, actionName)
			}
		}
		if err := validateActionInputSpecs("actions."+actionName+".params", act.Params); err != nil {
			return err
		}
		if err := validateActionInputSpecs("actions."+actionName+".extracts", act.Extracts); err != nil {
			return err
		}
	}
	if cfg.Login != nil {
		if err := validateLoginConfig(cfg.Login); err != nil {
			return err
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
		if act.Body != nil || len(act.Form) > 0 || len(act.Multipart) > 0 {
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
		Path:         cloneJSONValue(act.Path),
		Proxy:        proxy,
		Timeout:      timeout,
		Retries:      retries,
		Headers:      mergeMap(cfg.Headers, act.Headers),
		Cookies:      mergeMap(cfg.Cookies, act.Cookies),
		Query:        mergeMap(cfg.Query, act.Query),
		Body:         cloneJSONValue(act.Body),
		Form:         copyMap(act.Form),
		Multipart:    cloneMultipartParts(act.Multipart),
		Download:     cloneDownloadConfig(act.Download),
		ExpectStatus: expectStatus,
		Extractor:    extractor,
		Params:       cloneActionInputSpecs(act.Params),
		Extracts:     cloneActionInputSpecs(act.Extracts),
		Save:         copyStringMap(act.Save),
	}, nil
}

func validateMultipartPart(prefix string, part multipartPart) error {
	if strings.TrimSpace(part.Name) == "" {
		return fmt.Errorf("%w: %s.name is required", ErrConfig, prefix)
	}
	if hasControlCharacter(part.Name) {
		return fmt.Errorf("%w: %s.name contains a control character", ErrConfig, prefix)
	}
	if (part.Value == nil) == (part.File == nil) {
		return fmt.Errorf("%w: %s must set exactly one of value or file", ErrConfig, prefix)
	}
	if part.MaxBytes < 0 {
		return fmt.Errorf("%w: %s.max_bytes cannot be negative", ErrConfig, prefix)
	}
	if part.Value != nil && (part.Filename != nil || strings.TrimSpace(part.ContentType) != "" || part.MaxBytes != 0) {
		return fmt.Errorf("%w: %s filename, content_type, and max_bytes require file", ErrConfig, prefix)
	}
	return nil
}

func validateDownloadConfig(prefix string, download *downloadConfig) error {
	if download == nil {
		return nil
	}
	if download.Path == nil {
		return fmt.Errorf("%w: %s.path is required", ErrConfig, prefix)
	}
	if download.MaxBytes < 0 {
		return fmt.Errorf("%w: %s.max_bytes cannot be negative", ErrConfig, prefix)
	}
	return nil
}

func hasControlCharacter(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func cloneMultipartParts(parts []multipartPart) []multipartPart {
	if parts == nil {
		return nil
	}
	out := make([]multipartPart, len(parts))
	for index, part := range parts {
		out[index] = multipartPart{
			Name:        part.Name,
			Value:       cloneJSONValue(part.Value),
			File:        cloneJSONValue(part.File),
			Filename:    cloneJSONValue(part.Filename),
			ContentType: part.ContentType,
			MaxBytes:    part.MaxBytes,
		}
	}
	return out
}

func cloneDownloadConfig(download *downloadConfig) *downloadConfig {
	if download == nil {
		return nil
	}
	return &downloadConfig{
		Path:      cloneJSONValue(download.Path),
		Overwrite: cloneJSONValue(download.Overwrite),
		MaxBytes:  download.MaxBytes,
	}
}

func validateLoginConfig(login *loginConfig) error {
	if login == nil {
		return nil
	}
	if strings.TrimSpace(login.Path) == "" {
		return fmt.Errorf("%w: login.path is required", ErrConfig)
	}
	if _, err := normalizeStorageScope(login.SecretScope); err != nil {
		return fmt.Errorf("%w: login.secret_scope: %v", ErrConfig, err)
	}
	if _, err := normalizeExpectStatus(login.ExpectStatus); err != nil {
		return fmt.Errorf("%w: login.expect_status: %v", ErrConfig, err)
	}
	extractor, err := extractorFromLogin(login)
	if err != nil {
		return err
	}
	if _, err := compileExtractor("login", extractor, nil); err != nil {
		return err
	}
	if _, err := normalizeLoginBodyFormat(login.BodyFormat); err != nil {
		return err
	}
	return nil
}

func mergeLogin(cfg *configFile, timeoutOverride time.Duration) (mergedLogin, error) {
	if cfg.Login == nil {
		return mergedLogin{}, fmt.Errorf("%w: site does not define built-in basic login; use an external Python script for OIDC/SSO and other non-basic flows", ErrConfig)
	}

	expectStatus, err := normalizeExpectStatus(cfg.Login.ExpectStatus)
	if err != nil {
		return mergedLogin{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	if len(expectStatus) == 0 {
		expectStatus = []int{http.StatusOK}
	}

	timeout := cfg.Timeout.Duration
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	if timeoutOverride > 0 {
		timeout = timeoutOverride
	}

	retries := cfg.Retries
	if retries < 0 {
		return mergedLogin{}, fmt.Errorf("%w: retries cannot be negative", ErrConfig)
	}

	extractor, err := extractorFromLogin(cfg.Login)
	if err != nil {
		return mergedLogin{}, err
	}
	bodyFormat, err := normalizeLoginBodyFormat(cfg.Login.BodyFormat)
	if err != nil {
		return mergedLogin{}, err
	}
	secretScope, err := normalizeStorageScope(cfg.Login.SecretScope)
	if err != nil {
		return mergedLogin{}, err
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Login.Method))
	if method == "" {
		method = "POST"
	}

	usernameKey := strings.TrimSpace(cfg.Login.UsernameField)
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := strings.TrimSpace(cfg.Login.PasswordField)
	if passwordKey == "" {
		passwordKey = "password"
	}

	headers := map[string]any{}
	for key, value := range cfg.Login.Headers {
		headers[key] = cloneJSONValue(value)
	}

	return mergedLogin{
		Method:       method,
		Path:         cfg.Login.Path,
		SecretScope:  secretScope,
		BodyFormat:   bodyFormat,
		UsernameKey:  usernameKey,
		PasswordKey:  passwordKey,
		BasicAuth:    cfg.Login.BasicAuth,
		Headers:      headers,
		Timeout:      timeout,
		Retries:      retries,
		ExpectStatus: expectStatus,
		Extractor:    extractor,
		Save:         cloneStringMap(cfg.Login.Save),
	}, nil
}

func normalizeLoginBodyFormat(value string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(value))
	if format == "" {
		return "form", nil
	}
	switch format {
	case "form", "json":
		return format, nil
	default:
		return "", fmt.Errorf("%w: login.body_format must be form or json", ErrConfig)
	}
}

func validateActionPath(prefix string, raw any) error {
	switch path := raw.(type) {
	case string:
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%w: %s is required", ErrConfig, prefix)
		}
		return nil
	case map[string]any:
		if _, ok, err := parseSourceSpec(path); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: %s must be a string or dynamic source", ErrConfig, prefix)
		}
		return nil
	default:
		if raw == nil {
			return fmt.Errorf("%w: %s is required", ErrConfig, prefix)
		}
		return fmt.Errorf("%w: %s must be a string or dynamic source", ErrConfig, prefix)
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
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
