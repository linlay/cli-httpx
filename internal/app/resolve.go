package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const redactedValue = "***"

type resolver struct {
	state  *profileState
	reveal bool
}

type sourceSpec struct {
	From      string
	Key       string
	Path      string
	Cmd       string
	TimeoutMS int
	Trim      bool
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

func (r resolver) resolveSource(ctx context.Context, spec sourceSpec) (string, error) {
	if !r.reveal {
		return redactedValue, nil
	}

	switch spec.From {
	case "env":
		value, ok := os.LookupEnv(spec.Key)
		if !ok {
			return "", fmt.Errorf("%w: environment variable %q not set", ErrExecution, spec.Key)
		}
		return maybeTrim(value, spec.Trim), nil
	case "file":
		path, err := expandPath(spec.Path)
		if err != nil {
			return "", fmt.Errorf("%w: expand file path: %v", ErrExecution, err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%w: read file %q: %v", ErrExecution, path, err)
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
			return "", fmt.Errorf("%w: shell command failed: %s", ErrExecution, message)
		}
		return maybeTrim(stdout.String(), spec.Trim), nil
	case "state":
		value, ok := r.state.Values[spec.Key]
		if !ok {
			return "", fmt.Errorf("%w: state key %q not found", ErrExecution, spec.Key)
		}
		return maybeTrim(value, spec.Trim), nil
	default:
		return "", fmt.Errorf("%w: unsupported source %q", ErrConfig, spec.From)
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
