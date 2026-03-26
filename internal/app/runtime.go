package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

type Runtime struct {
	stdout io.Writer
	stderr io.Writer
}

type compiledRequest struct {
	Profile      string            `json:"profile"`
	Action       string            `json:"action"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Proxy        string            `json:"proxy,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Cookies      map[string]string `json:"cookies,omitempty"`
	Body         any               `json:"body,omitempty"`
	TimeoutMS    int64             `json:"timeout_ms"`
	Retries      int               `json:"retries"`
	ExpectStatus []int             `json:"expect_status,omitempty"`
	Extract      string            `json:"extract,omitempty"`
	Save         map[string]string `json:"save,omitempty"`

	BodyBytes   []byte `json:"-"`
	ContentType string `json:"-"`
}

type requestOutcome struct {
	Envelope envelope
	RawBody  []byte
	ExitCode int
}

func NewRuntime(stdout, stderr io.Writer) *Runtime {
	return &Runtime{
		stdout: stdout,
		stderr: stderr,
	}
}

func (rt *Runtime) Run(req commandRequest) int {
	configPath, err := resolveConfigPath(req.Options.ConfigDir, req.Profile)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitConfig, "config_error", err.Error())
	}

	state, err := loadState(req.Options.StateDir, req.Profile)
	if err != nil {
		return rt.writeFailure(req, nil, nil, nil, ExitExecution, "state_error", err.Error())
	}

	compiled, jar, state, err := rt.compile(req, cfg, state)
	if err != nil {
		exitCode, code := classifyError(err)
		return rt.writeFailure(req, nil, nil, nil, exitCode, code, err.Error())
	}

	if req.Options.Inspect {
		if err := writeJSON(rt.stdout, compiled); err != nil {
			fmt.Fprintf(rt.stderr, "error: %v\n", err)
			return ExitExecution
		}
		return ExitSuccess
	}

	outcome := rt.execute(req, compiled, jar, state)
	if req.Options.Format == formatBody {
		if outcome.ExitCode == ExitSuccess {
			if _, err := rt.stdout.Write(outcome.RawBody); err != nil {
				fmt.Fprintf(rt.stderr, "error: %v\n", err)
				return ExitExecution
			}
		} else if outcome.Envelope.Error != nil {
			fmt.Fprintf(rt.stderr, "error: %s\n", outcome.Envelope.Error.Message)
		}
		return outcome.ExitCode
	}

	if err := writeJSON(rt.stdout, outcome.Envelope); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return outcome.ExitCode
}

func (rt *Runtime) compile(req commandRequest, cfg *configFile, state *profileState) (*compiledRequest, *persistentJar, *profileState, error) {
	actionName := req.Action
	if req.Action == "login" {
		if strings.TrimSpace(cfg.LoginAction) == "" {
			return nil, nil, nil, fmt.Errorf("%w: profile %q does not define login_action", ErrConfig, req.Profile)
		}
		actionName = cfg.LoginAction
	}

	act, err := selectAction(cfg, req.Profile, actionName)
	if err != nil {
		return nil, nil, nil, err
	}

	merged, err := mergeAction(actionName, cfg, act, req.Options.Timeout)
	if err != nil {
		return nil, nil, nil, err
	}

	res := resolver{
		state:  state,
		reveal: !req.Options.Inspect || req.Options.Reveal,
		params: req.Options.Params,
	}
	ctx := context.Background()

	headers := map[string]string{}
	for key, raw := range merged.Headers {
		resolved, err := res.resolveAny(ctx, raw)
		if err != nil {
			return nil, nil, nil, err
		}
		value, err := stringifyScalar(resolved)
		if err != nil {
			return nil, nil, nil, err
		}
		if req.Options.Inspect && !req.Options.Reveal && isSensitiveHeader(key) {
			value = redactedValue
		}
		headers[key] = value
	}

	cookies := map[string]string{}
	for key, raw := range merged.Cookies {
		resolved, err := res.resolveAny(ctx, raw)
		if err != nil {
			return nil, nil, nil, err
		}
		value, err := stringifyScalar(resolved)
		if err != nil {
			return nil, nil, nil, err
		}
		cookies[key] = value
	}

	proxyValue := ""
	if merged.Proxy != nil {
		resolved, err := res.resolveAny(ctx, merged.Proxy)
		if err != nil {
			return nil, nil, nil, err
		}
		value, err := stringifyScalar(resolved)
		if err != nil {
			return nil, nil, nil, err
		}
		value = strings.TrimSpace(value)
		if value != "" && !(req.Options.Inspect && !req.Options.Reveal && value == redactedValue) {
			if _, err := parseProxyURL(value); err != nil {
				return nil, nil, nil, fmt.Errorf("%w: proxy: %v", ErrConfig, err)
			}
		}
		proxyValue = value
	}

	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: parse base_url: %v", ErrConfig, err)
	}
	pathURL, err := url.Parse(merged.Path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: parse action path: %v", ErrConfig, err)
	}
	finalURL := baseURL.ResolveReference(pathURL)
	query := finalURL.Query()
	for key, raw := range merged.Query {
		resolved, err := res.resolveAny(ctx, raw)
		if err != nil {
			return nil, nil, nil, err
		}
		value, err := stringifyScalar(resolved)
		if err != nil {
			return nil, nil, nil, err
		}
		query.Set(key, value)
	}
	finalURL.RawQuery = query.Encode()

	var bodyValue any
	var bodyBytes []byte
	contentType := ""
	if len(merged.Form) > 0 {
		form := url.Values{}
		out := map[string]string{}
		for key, raw := range merged.Form {
			resolved, err := res.resolveAny(ctx, raw)
			if err != nil {
				return nil, nil, nil, err
			}
			value, err := stringifyFormValue(resolved)
			if err != nil {
				return nil, nil, nil, err
			}
			form.Set(key, value)
			out[key] = value
		}
		bodyBytes = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
		bodyValue = out
	} else if merged.Body != nil {
		resolved, err := res.resolveAny(ctx, merged.Body)
		if err != nil {
			return nil, nil, nil, err
		}
		bodyValue = resolved
		bodyBytes, contentType, err = bodyToBytes(resolved)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	if _, ok := headers["Content-Type"]; !ok && contentType != "" {
		headers["Content-Type"] = contentType
	}

	jar := newPersistentJar(state.Cookies)
	compiledProxy := proxyValue
	if req.Options.Inspect && !req.Options.Reveal {
		compiledProxy = redactProxyURL(proxyValue)
	}
	return &compiledRequest{
		Profile:      req.Profile,
		Action:       actionName,
		Method:       merged.Method,
		URL:          finalURL.String(),
		Proxy:        compiledProxy,
		Headers:      headers,
		Cookies:      cookies,
		Body:         bodyValue,
		TimeoutMS:    merged.Timeout.Milliseconds(),
		Retries:      merged.Retries,
		ExpectStatus: merged.ExpectStatus,
		Extract:      merged.Extract,
		Save:         merged.Save,
		BodyBytes:    bodyBytes,
		ContentType:  contentType,
	}, jar, state, nil
}

func (rt *Runtime) execute(req commandRequest, compiled *compiledRequest, jar *persistentJar, state *profileState) requestOutcome {
	client, err := newHTTPClient(compiled, jar)
	if err != nil {
		exitCode, code := classifyError(err)
		return requestOutcome{
			Envelope: envelope{
				OK:      false,
				Profile: req.Profile,
				Action:  compiled.Action,
				Error: &errorEnvelope{
					Code:    code,
					Message: err.Error(),
				},
			},
			ExitCode: exitCode,
		}
	}

	var lastErr error
	for attempt := 0; attempt <= compiled.Retries; attempt++ {
		outcome, retry, err := rt.performOnce(client, req, compiled, jar, state)
		if err == nil {
			return outcome
		}
		if !retry && outcome.ExitCode != 0 {
			return outcome
		}
		lastErr = err
		if !retry || attempt == compiled.Retries {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}

	exitCode, code := classifyError(lastErr)
	return requestOutcome{
		Envelope: envelope{
			OK:      false,
			Profile: req.Profile,
			Action:  compiled.Action,
			Error: &errorEnvelope{
				Code:    code,
				Message: lastErr.Error(),
			},
		},
		ExitCode: exitCode,
	}
}

func newHTTPClient(compiled *compiledRequest, jar *persistentJar) (*http.Client, error) {
	transport, err := buildTransport(compiled.Proxy)
	if err != nil {
		return nil, fmt.Errorf("%w: proxy: %v", ErrConfig, err)
	}
	return &http.Client{
		Jar:       jar,
		Timeout:   time.Duration(compiled.TimeoutMS) * time.Millisecond,
		Transport: transport,
	}, nil
}

func buildTransport(proxyAddress string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyAddress) == "" {
		transport.Proxy = http.ProxyFromEnvironment
		return transport, nil
	}
	proxyURL, err := parseProxyURL(proxyAddress)
	if err != nil {
		return nil, err
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport, nil
}

func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("proxy URL must include scheme and host")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}
}

func redactProxyURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return redactedValue
	}
	if parsed.User == nil {
		return raw
	}
	if _, ok := parsed.User.Password(); ok {
		parsed.User = url.UserPassword(redactedValue, redactedValue)
	} else {
		parsed.User = url.User(redactedValue)
	}
	return parsed.String()
}

func (rt *Runtime) performOnce(client *http.Client, req commandRequest, compiled *compiledRequest, jar *persistentJar, state *profileState) (requestOutcome, bool, error) {
	request, err := http.NewRequest(compiled.Method, compiled.URL, bytes.NewReader(compiled.BodyBytes))
	if err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: build request: %v", ErrExecution, err)
	}
	for key, value := range compiled.Headers {
		request.Header.Set(key, value)
	}
	if len(compiled.Cookies) > 0 {
		for name, value := range compiled.Cookies {
			request.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}

	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return requestOutcome{}, true, fmt.Errorf("%w: send request: %v", ErrExecution, err)
	}
	defer response.Body.Close()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: read response body: %v", ErrExecution, err)
	}
	duration := time.Since(start)

	decodedBody := decodeResponseBody(response, bodyBytes)
	headers := cloneHeaders(response.Header)
	ctxValue := responseContext(response.StatusCode, headers, decodedBody, bodyBytes)

	if response.StatusCode >= 500 && compiled.Retries > 0 {
		if !matchesExpectedStatus(response.StatusCode, compiled.ExpectStatus) {
			return requestOutcome{}, true, fmt.Errorf("%w: received status %d", ErrExecution, response.StatusCode)
		}
	}

	extracted, err := evaluateMaybe(compiled.Extract, ctxValue)
	if err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: %v", ErrAssertion, err)
	}

	updatedKeys := []string{}
	for key, expr := range compiled.Save {
		value, err := evaluateRequired(expr, ctxValue)
		if err != nil {
			return requestOutcome{}, false, fmt.Errorf("%w: save %q: %v", ErrAssertion, key, err)
		}
		asString, err := renderStateValue(value)
		if err != nil {
			return requestOutcome{}, false, fmt.Errorf("%w: save %q: %v", ErrAssertion, key, err)
		}
		state.Values[key] = asString
		updatedKeys = append(updatedKeys, key)
	}
	sort.Strings(updatedKeys)

	state.Cookies = jar.Snapshot()
	if req.Action == "login" {
		state.LastLogin = time.Now().UTC().Format(time.RFC3339)
	}
	if err := saveState(req.Options.StateDir, req.Profile, state); err != nil {
		return requestOutcome{}, false, fmt.Errorf("%w: persist state: %v", ErrExecution, err)
	}

	ok := matchesExpectedStatus(response.StatusCode, compiled.ExpectStatus)
	envelopeValue := envelope{
		OK:           ok,
		Profile:      req.Profile,
		Action:       compiled.Action,
		Status:       response.StatusCode,
		DurationMS:   duration.Milliseconds(),
		Headers:      headers,
		Body:         decodedBody,
		Extract:      extracted,
		StateUpdated: updatedKeys,
	}

	if !ok {
		envelopeValue.Error = &errorEnvelope{
			Code:    "assertion_error",
			Message: fmt.Sprintf("unexpected status %d", response.StatusCode),
		}
		return requestOutcome{Envelope: envelopeValue, RawBody: bodyBytes, ExitCode: ExitAssertion}, false, fmt.Errorf("%w: unexpected status %d", ErrAssertion, response.StatusCode)
	}
	return requestOutcome{Envelope: envelopeValue, RawBody: bodyBytes, ExitCode: ExitSuccess}, false, nil
}

func classifyError(err error) (int, string) {
	switch {
	case err == nil:
		return ExitSuccess, ""
	case errors.Is(err, ErrConfig):
		return ExitConfig, "config_error"
	case errors.Is(err, ErrAssertion):
		return ExitAssertion, "assertion_error"
	default:
		return ExitExecution, "execution_error"
	}
}

func (rt *Runtime) writeFailure(req commandRequest, headers map[string][]string, body any, rawBody []byte, exitCode int, code, message string) int {
	if req.Options.Format == formatBody {
		fmt.Fprintf(rt.stderr, "error: %s\n", message)
		return exitCode
	}
	env := envelope{
		OK:      false,
		Profile: req.Profile,
		Action:  req.Action,
		Headers: headers,
		Body:    body,
		Error: &errorEnvelope{
			Code:    code,
			Message: message,
		},
	}
	if err := writeJSON(rt.stdout, env); err != nil {
		fmt.Fprintf(rt.stderr, "error: %v\n", err)
		return ExitExecution
	}
	return exitCode
}

func responseContext(status int, headers map[string][]string, body any, rawBody []byte) map[string]any {
	lowerHeaders := make(map[string][]string, len(headers))
	for key, values := range headers {
		lowerHeaders[strings.ToLower(key)] = values
	}
	return map[string]any{
		"status":        status,
		"headers":       headers,
		"headers_lower": lowerHeaders,
		"body":          body,
		"body_text":     string(rawBody),
	}
}

func evaluateMaybe(expr string, input any) (any, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, nil
	}
	return runJQ(expr, input, false)
}

func evaluateRequired(expr string, input any) (any, error) {
	return runJQ(expr, input, true)
}

func runJQ(expr string, input any, requireResult bool) (any, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("parse jq: %v", err)
	}
	iter := query.Run(input)
	results := []any{}
	for {
		value, ok := iter.Next()
		if !ok {
			break
		}
		if errValue, ok := value.(error); ok {
			return nil, fmt.Errorf("run jq: %v", errValue)
		}
		results = append(results, value)
	}
	if len(results) == 0 {
		if requireResult {
			return nil, fmt.Errorf("jq expression returned no results")
		}
		return nil, nil
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

func renderStateValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool, int, int8, int16, int32, int64, float32, float64:
		return fmt.Sprint(typed), nil
	default:
		content, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}
}

func decodeResponseBody(response *http.Response, body []byte) any {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "json") || json.Valid(body) {
		var decoded any
		if err := json.Unmarshal(body, &decoded); err == nil {
			return decoded
		}
	}
	return string(body)
}

func cloneHeaders(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string][]string, len(header))
	for key, values := range header {
		cp := make([]string, len(values))
		copy(cp, values)
		out[key] = cp
	}
	return out
}

func matchesExpectedStatus(status int, expected []int) bool {
	if len(expected) == 0 {
		return status >= 200 && status < 300
	}
	for _, candidate := range expected {
		if status == candidate {
			return true
		}
	}
	return false
}

func isSensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "set-cookie", "proxy-authorization", "x-api-key":
		return true
	default:
		return false
	}
}
