package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const redactedValue = "***"

type resolver struct {
	state  *profileState
	reveal bool
	params map[string]string
	site   string
}

type sourceSpec struct {
	From      string
	Key       string
	Path      string
	Cmd       string
	TimeoutMS int
	Trim      bool
	Value     any
	Default   any
}

func (r resolver) resolveAny(ctx context.Context, value any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		if spec, ok, err := parseSourceSpec(typed); err != nil {
			return nil, err
		} else if ok {
			return r.resolveSource(ctx, spec)
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := r.resolveAny(ctx, item)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			resolved, err := r.resolveAny(ctx, item)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return typed, nil
	}
}

func parseSourceSpec(input map[string]any) (sourceSpec, bool, error) {
	rawFrom, ok := input["from"]
	if !ok {
		return sourceSpec{}, false, nil
	}
	from, ok := rawFrom.(string)
	if !ok {
		return sourceSpec{}, false, fmt.Errorf("%w: dynamic source 'from' must be a string", ErrConfig)
	}
	spec := sourceSpec{From: from}
	switch from {
	case "literal":
		if err := rejectUnknownSourceKeys(input, "from", "value"); err != nil {
			return sourceSpec{}, false, err
		}
		value, ok := input["value"]
		if !ok {
			return sourceSpec{}, false, fmt.Errorf("%w: literal source requires value", ErrConfig)
		}
		spec.Value = value
	case "param":
		if err := rejectUnknownSourceKeys(input, "from", "key", "default", "trim"); err != nil {
			return sourceSpec{}, false, err
		}
		spec.Key, ok = input["key"].(string)
		if !ok || spec.Key == "" {
			return sourceSpec{}, false, fmt.Errorf("%w: param source requires non-empty key", ErrConfig)
		}
		if value, ok := input["default"]; ok {
			spec.Default = value
		}
	case "env":
		if err := rejectUnknownSourceKeys(input, "from", "key", "trim"); err != nil {
			return sourceSpec{}, false, err
		}
		spec.Key, ok = input["key"].(string)
		if !ok || spec.Key == "" {
			return sourceSpec{}, false, fmt.Errorf("%w: env source requires non-empty key", ErrConfig)
		}
	case "file":
		if err := rejectUnknownSourceKeys(input, "from", "path", "trim"); err != nil {
			return sourceSpec{}, false, err
		}
		spec.Path, ok = input["path"].(string)
		if !ok || spec.Path == "" {
			return sourceSpec{}, false, fmt.Errorf("%w: file source requires non-empty path", ErrConfig)
		}
	case "shell":
		if err := rejectUnknownSourceKeys(input, "from", "cmd", "timeout_ms", "trim"); err != nil {
			return sourceSpec{}, false, err
		}
		spec.Cmd, ok = input["cmd"].(string)
		if !ok || spec.Cmd == "" {
			return sourceSpec{}, false, fmt.Errorf("%w: shell source requires non-empty cmd", ErrConfig)
		}
		if timeout, ok := integerValue(input["timeout_ms"]); ok {
			spec.TimeoutMS = timeout
		}
	case "state":
		if err := rejectUnknownSourceKeys(input, "from", "key", "trim"); err != nil {
			return sourceSpec{}, false, err
		}
		spec.Key, ok = input["key"].(string)
		if !ok || spec.Key == "" {
			return sourceSpec{}, false, fmt.Errorf("%w: state source requires non-empty key", ErrConfig)
		}
	default:
		return sourceSpec{}, false, fmt.Errorf("%w: unsupported source %q", ErrConfig, from)
	}
	if trim, ok := input["trim"].(bool); ok {
		spec.Trim = trim
	}
	return spec, true, nil
}

func rejectUnknownSourceKeys(input map[string]any, allowed ...string) error {
	allowlist := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowlist[key] = struct{}{}
	}
	for key := range input {
		if _, ok := allowlist[key]; !ok {
			return fmt.Errorf("%w: unsupported field %q in dynamic source", ErrConfig, key)
		}
	}
	return nil
}

func (r resolver) resolveSource(ctx context.Context, spec sourceSpec) (any, error) {
	if !r.reveal && spec.From != "literal" {
		if spec.From == "param" {
			if _, ok := r.params[spec.Key]; !ok && spec.Default != nil {
				return maybeTrimValue(spec.Default, spec.Trim), nil
			}
		}
		return redactedValue, nil
	}

	switch spec.From {
	case "literal":
		return spec.Value, nil
	case "param":
		if value, ok := r.params[spec.Key]; ok {
			value = maybeTrim(value, spec.Trim)
			if spec.Default != nil {
				coerced, err := coerceToSampleType(value, spec.Default)
				if err != nil {
					return nil, fmt.Errorf("%w: parameter %q: %v", ErrExecution, spec.Key, err)
				}
				return coerced, nil
			}
			return value, nil
		}
		if spec.Default != nil {
			return maybeTrimValue(spec.Default, spec.Trim), nil
		}
		return nil, fmt.Errorf("%w: parameter %q not provided", ErrExecution, spec.Key)
	case "env":
		tried := []string{}
		if r.site != "" {
			siteKey := secretEnvKey(r.site, spec.Key)
			tried = append(tried, siteKey)
			if value, ok := os.LookupEnv(siteKey); ok {
				return maybeTrim(value, spec.Trim), nil
			}
		}
		tried = append(tried, spec.Key)
		if value, ok := os.LookupEnv(spec.Key); ok {
			return maybeTrim(value, spec.Trim), nil
		}
		if r.site != "" {
			return nil, fmt.Errorf("%w: env var %q not set; tried %q, %q (hint: run 'eval $(httpx load %s)' to load secrets)", ErrExecution, tried[0], tried[0], spec.Key, r.site)
		}
		return nil, fmt.Errorf("%w: env var %q not set", ErrExecution, spec.Key)
	case "file":
		path, err := expandPath(spec.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: expand file path: %v", ErrExecution, err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read file %q: %v", ErrExecution, path, err)
		}
		return maybeTrim(string(content), spec.Trim), nil
	case "shell":
		timeout := 5 * time.Second
		if spec.TimeoutMS > 0 {
			timeout = time.Duration(spec.TimeoutMS) * time.Millisecond
		}
		execCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		cmd := exec.CommandContext(execCtx, "/bin/sh", "-lc", spec.Cmd)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = err.Error()
			}
			return nil, fmt.Errorf("%w: shell command failed: %s", ErrExecution, message)
		}
		return maybeTrim(stdout.String(), spec.Trim), nil
	case "state":
		value, ok := r.state.Values[spec.Key]
		if !ok {
			return nil, fmt.Errorf("%w: state key %q not found", ErrExecution, spec.Key)
		}
		return maybeTrim(value, spec.Trim), nil
	default:
		return nil, fmt.Errorf("%w: unsupported source %q", ErrConfig, spec.From)
	}
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func maybeTrim(value string, trim bool) string {
	if trim {
		return strings.TrimSpace(value)
	}
	return value
}

func maybeTrimValue(value any, trim bool) any {
	asString, ok := value.(string)
	if !ok {
		return value
	}
	return maybeTrim(asString, trim)
}

func coerceToSampleType(raw string, sample any) (any, error) {
	switch sample.(type) {
	case string:
		return raw, nil
	case int:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, err
		}
		return int(value), nil
	case int8:
		value, err := strconv.ParseInt(raw, 10, 8)
		if err != nil {
			return nil, err
		}
		return int8(value), nil
	case int16:
		value, err := strconv.ParseInt(raw, 10, 16)
		if err != nil {
			return nil, err
		}
		return int16(value), nil
	case int32:
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return nil, err
		}
		return int32(value), nil
	case int64:
		return strconv.ParseInt(raw, 10, 64)
	case float32:
		value, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return nil, err
		}
		return float32(value), nil
	case float64:
		return strconv.ParseFloat(raw, 64)
	case bool:
		return strconv.ParseBool(raw)
	default:
		return raw, nil
	}
}

func stringifyScalar(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	case bool, int, int8, int16, int32, int64, float32, float64:
		return fmt.Sprint(typed), nil
	default:
		return "", fmt.Errorf("%w: expected scalar value, got %T", ErrConfig, value)
	}
}

func stringifyFormValue(value any) (string, error) {
	switch value.(type) {
	case map[string]any, []any:
		content, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("%w: encode form value: %v", ErrConfig, err)
		}
		return string(content), nil
	default:
		return stringifyScalar(value)
	}
}

func bodyToBytes(value any) ([]byte, string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, "", nil
	case string:
		return []byte(typed), "text/plain; charset=utf-8", nil
	case []byte:
		return typed, "application/octet-stream", nil
	default:
		content, err := json.Marshal(typed)
		if err != nil {
			return nil, "", fmt.Errorf("%w: encode body: %v", ErrConfig, err)
		}
		return content, "application/json", nil
	}
}
