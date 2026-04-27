package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

	return cmd
}

func runLoad(stdout io.Writer, site string, opts globalOptions) error {
	content, path, err := findSecretFile(opts.SecretDir, site)
	if err != nil {
		return err
	}

	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return fmt.Errorf("%w: invalid secret JSON at %q: %v; expected a JSON object like {\"cookie\":\"...\"}", ErrConfig, path, err)
	}
	if data == nil {
		return fmt.Errorf("%w: invalid secret JSON at %q: expected a JSON object like {\"cookie\":\"...\"}", ErrConfig, path)
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
	configPath, configAliases, err := loadedConfigExports(opts.ConfigDir, site)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "export %s=%s\n", siteConfigEnvKey(site), shellQuote(configPath))
	for _, alias := range configAliases {
		fmt.Fprintf(stdout, "export %s=%s\n", alias.Key, shellQuote(alias.Value))
	}

	return nil
}

type envExport struct {
	Key   string
	Value string
}

func loadedConfigExports(configDir, site string) (string, []envExport, error) {
	if strings.TrimSpace(configDir) == "" {
		return site + ".toml", nil, nil
	}
	matches, err := findSiteConfigFiles(configDir, site)
	if err != nil {
		return "", nil, err
	}
	if len(matches) == 0 {
		return filepath.Join(configDir, site+".toml"), nil, nil
	}

	configPath := ""
	aliases := []envExport{}
	for _, match := range matches {
		configDirForSite := filepath.Dir(match)
		relDir, err := filepath.Rel(configDir, configDirForSite)
		if err != nil {
			return "", nil, fmt.Errorf("%w: resolve config directory %q relative to %q: %v", ErrConfig, configDirForSite, configDir, err)
		}
		if relDir == "." {
			configPath = match
			continue
		}
		aliases = append(aliases, envExport{
			Key:   siteConfigDirEnvKey(site, relDir),
			Value: match,
		})
	}
	if configPath == "" {
		configPath = matches[0]
	}
	return configPath, aliases, nil
}

func findSiteConfigFiles(configDir, site string) ([]string, error) {
	target := site + ".toml"
	var matches []string
	err := filepath.WalkDir(configDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%w: read config path %q: %v", ErrConfig, path, err)
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == target {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func findSecretFile(dir, site string) ([]byte, string, error) {
	path := filepath.Join(dir, site+".json")
	content, err := os.ReadFile(path)
	if err == nil {
		return content, path, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("%w: read secret file %q: %v", ErrExecution, path, err)
	}

	return nil, "", fmt.Errorf("%w: secret file not found at %q; create it with: mkdir -p %q && cat > %q; expected JSON object like {\"cookie\":\"...\"}", ErrConfig, path, dir, path)
}

func loadSecretKey(dir, site, key string) (any, error) {
	content, path, err := findSecretFile(dir, site)
	if err != nil {
		return nil, err
	}

	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("%w: invalid secret JSON at %q: %v; expected a JSON object like {\"cookie\":\"...\"}", ErrConfig, path, err)
	}
	if data == nil {
		return nil, fmt.Errorf("%w: invalid secret JSON at %q: expected a JSON object like {\"cookie\":\"...\"}", ErrConfig, path)
	}
	value, ok := data[key]
	if !ok {
		return nil, fmt.Errorf("%w: secret key %q not found in %q", ErrExecution, key, path)
	}
	return value, nil
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

func siteConfigDirEnvKey(site, relDir string) string {
	key := strings.ReplaceAll(filepath.ToSlash(relDir), "/", ".")
	return secretEnvKey(site, key+".config")
}
