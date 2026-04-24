package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type siteSecret struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func loadSecret(dir, site string) (*siteSecret, error) {
	path := secretPath(dir, site)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: secret file %q not found", ErrExecution, path)
		}
		return nil, fmt.Errorf("%w: read secret file %q: %v", ErrExecution, path, err)
	}

	var secret siteSecret
	if err := json.Unmarshal(content, &secret); err != nil {
		return nil, fmt.Errorf("%w: decode secret file %q: %v", ErrExecution, path, err)
	}
	if strings.TrimSpace(secret.Username) == "" {
		return nil, fmt.Errorf("%w: secret file %q is missing username", ErrExecution, path)
	}
	if strings.TrimSpace(secret.Password) == "" {
		return nil, fmt.Errorf("%w: secret file %q is missing password", ErrExecution, path)
	}
	return &secret, nil
}

func secretPath(dir, site string) string {
	return filepath.Join(dir, site+".json")
}

func newLoadCommand(options *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load <site>",
		Short: "Load site secrets as environment variables for use with from=env",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			site := args[0]
			if err := validateSiteName(site); err != nil {
				return err
			}
			return runLoad(cmd.OutOrStdout(), site, options.snapshot())
		},
	}

	cmd.Flags().StringVar(&options.global.SecretDir, "secret", options.global.SecretDir, "Secret directory or file path")

	return cmd
}

func runLoad(stdout io.Writer, site string, opts globalOptions) error {
	content, path, err := findSecretFile(opts.SecretDir, site)
	if err != nil {
		return err
	}

	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return fmt.Errorf("%w: decode secret file %q: %v", ErrExecution, path, err)
	}

	prefix := site
	for key, value := range data {
		envKey := secretEnvKey(prefix, key)
		envValue, err := stringifyEnvValue(value)
		if err != nil {
			return fmt.Errorf("%w: secret key %q: %v", ErrExecution, key, err)
		}
		fmt.Fprintf(stdout, "export %s=%s\n", envKey, shellQuote(envValue))
	}
	fmt.Fprintf(stdout, "export %s=%s\n", siteConfigEnvKey(site), shellQuote(filepath.Join(opts.ConfigDir, site+".toml")))

	return nil
}

func findSecretFile(dir, site string) ([]byte, string, error) {
	info, statErr := os.Stat(dir)
	if statErr == nil && !info.IsDir() {
		content, err := os.ReadFile(dir)
		if err != nil {
			return nil, "", fmt.Errorf("%w: read secret file %q: %v", ErrExecution, dir, err)
		}
		return content, dir, nil
	}

	path := filepath.Join(dir, site+".json")
	content, err := os.ReadFile(path)
	if err == nil {
		return content, path, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("%w: read secret file %q: %v", ErrExecution, path, err)
	}

	return nil, "", fmt.Errorf("%w: secret file not found at %q (hint: use --secret to specify path)", ErrConfig, path)
}

func stringifyEnvValue(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case float64:
		return fmt.Sprintf("%v", v), nil
	case bool:
		return fmt.Sprintf("%v", v), nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func shellQuote(s string) string {
	s = strings.ReplaceAll(s, "'", "'\\''")
	return "'" + s + "'"
}

func secretEnvKey(site, key string) string {
	return strings.ReplaceAll(site+"."+key, ".", "_")
}

func siteConfigEnvKey(site string) string {
	return secretEnvKey(site, "config")
}
