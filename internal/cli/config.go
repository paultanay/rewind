package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds the complete runtime configuration for a rewind session.
// Values are populated from (in ascending precedence):
//  1. Built-in defaults
//  2. rewind.yaml (or --config path)
//  3. REWIND_* environment variables
//  4. CLI flags
//
// Source endpoint sections use the same key structure as the YAML — see
// docs/config-reference.md for the full schema.
type Config struct {
	// SourceTimeout is the per-source collection timeout.
	SourceTimeout time.Duration `mapstructure:"source_timeout"`

	Prometheus PrometheusConfig   `mapstructure:"prometheus"`
	Loki       LokiConfig         `mapstructure:"loki"`
	Kubernetes KubernetesConfig   `mapstructure:"kubernetes"`
	Tempo      TempoConfig        `mapstructure:"tempo"`
	GitHub     GitHubConfig       `mapstructure:"github"`
	GitLab     GitLabConfig       `mapstructure:"gitlab"`
	AlertMgr   AlertManagerConfig `mapstructure:"alertmanager"`
}

type PrometheusConfig struct {
	URL      string            `mapstructure:"url"`
	Headers  map[string]string `mapstructure:"headers"`
	Disabled bool              `mapstructure:"disabled"`
}

type LokiConfig struct {
	URL            string `mapstructure:"url"`
	TenantID       string `mapstructure:"tenant_id"`
	Username       string `mapstructure:"username"`
	Password       string `mapstructure:"password"`
	GrafanaBaseURL string `mapstructure:"grafana_base_url"`
	MaxSampleLines int    `mapstructure:"max_sample_lines"`
	Disabled       bool   `mapstructure:"disabled"`
}

type KubernetesConfig struct {
	Kubeconfig string `mapstructure:"kubeconfig"`
	Context    string `mapstructure:"context"`
	Disabled   bool   `mapstructure:"disabled"`
}

type TempoConfig struct {
	URL            string `mapstructure:"url"`
	TenantID       string `mapstructure:"tenant_id"`
	Username       string `mapstructure:"username"`
	Password       string `mapstructure:"password"`
	GrafanaBaseURL string `mapstructure:"grafana_base_url"`
	Disabled       bool   `mapstructure:"disabled"`
}

type GitHubConfig struct {
	Token    string   `mapstructure:"token"`
	Repos    []string `mapstructure:"repos"`
	Disabled bool     `mapstructure:"disabled"`
}

type GitLabConfig struct {
	URL      string   `mapstructure:"url"`
	Token    string   `mapstructure:"token"`
	Projects []string `mapstructure:"projects"`
	Disabled bool     `mapstructure:"disabled"`
}

type AlertManagerConfig struct {
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Disabled bool   `mapstructure:"disabled"`
}

// LoadConfig reads configuration from the given file path (or searches for
// rewind.yaml in the working directory and ~/.config/rewind/ if path is empty).
// It does NOT apply CLI flag overrides — those are handled by Cobra bindings
// on each command.
func LoadConfig(path string) (*Config, error) {
	v := viper.New()

	// Defaults.
	v.SetDefault("source_timeout", "15s")
	v.SetDefault("prometheus.url", "http://localhost:9090")

	// Environment variables: REWIND_PROMETHEUS_URL etc.
	v.SetEnvPrefix("REWIND")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, key := range []string{
		"source_timeout",
		"prometheus.url", "prometheus.disabled",
		"loki.url", "loki.tenant_id", "loki.username", "loki.password", "loki.grafana_base_url", "loki.max_sample_lines", "loki.disabled",
		"kubernetes.kubeconfig", "kubernetes.context", "kubernetes.disabled",
		"tempo.url", "tempo.tenant_id", "tempo.username", "tempo.password", "tempo.grafana_base_url", "tempo.disabled",
		"github.token", "github.disabled",
		"gitlab.url", "gitlab.token", "gitlab.disabled",
		"alertmanager.url", "alertmanager.username", "alertmanager.password", "alertmanager.disabled",
	} {
		if err := v.BindEnv(key); err != nil {
			return nil, fmt.Errorf("config: bind environment %s: %w", key, err)
		}
	}

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("rewind")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			v.AddConfigPath(home + "/.config/rewind")
		}
	}

	// Missing config file is not an error — defaults suffice for `rewind demo`.
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config unmarshal: %w", err)
	}
	return &cfg, nil
}
