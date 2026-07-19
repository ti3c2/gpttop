package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"gpttop/internal/domain"
)

const redacted = "<redacted>"

type EnvLookup func(string) (string, bool)

type EndpointFlag struct {
	Name string
	URL  string
}

type EndpointFlags []EndpointFlag

func (f *EndpointFlags) Set(value string) error {
	ep, err := ParseEndpointFlag(value)
	if err != nil {
		return err
	}
	*f = append(*f, ep)
	return nil
}

func (f EndpointFlags) String() string {
	parts := make([]string, 0, len(f))
	for _, ep := range f {
		if ep.Name == "" {
			parts = append(parts, ep.URL)
			continue
		}
		parts = append(parts, ep.Name+"="+ep.URL)
	}
	return strings.Join(parts, ",")
}

func (f EndpointFlags) Type() string {
	return "endpoint"
}

type Overrides struct {
	Endpoints       []EndpointFlag
	RefreshInterval *time.Duration
	ScrapeTimeout   *time.Duration
	CurrentWindow   *time.Duration
	History         *time.Duration
	Windows         []time.Duration
	NoColor         *bool
	Demo            *bool
}

type rawConfig struct {
	RefreshInterval string        `yaml:"refresh_interval"`
	ScrapeTimeout   string        `yaml:"scrape_timeout"`
	CurrentWindow   string        `yaml:"current_window"`
	History         string        `yaml:"history"`
	Windows         []string      `yaml:"windows"`
	Endpoints       []rawEndpoint `yaml:"endpoints"`
	NoColor         *bool         `yaml:"no_color"`
	Demo            *bool         `yaml:"demo"`
}

type rawEndpoint struct {
	Name        string            `yaml:"name"`
	URL         string            `yaml:"url"`
	MetricsPath string            `yaml:"metrics_path"`
	Model       string            `yaml:"model"`
	Headers     map[string]string `yaml:"headers"`
}

func Defaults() domain.RuntimeConfig {
	return domain.RuntimeConfig{
		RefreshInterval: domain.DefaultInterval,
		ScrapeTimeout:   domain.DefaultTimeout,
		CurrentWindow:   domain.DefaultCurrentWindow,
		History:         domain.DefaultHistory,
		Windows:         append([]time.Duration(nil), domain.DefaultWindows...),
	}
}

func LoadFile(path string) (domain.RuntimeConfig, error) {
	return LoadFileWithEnv(path, os.LookupEnv)
}

func LoadFileWithEnv(path string, lookup EnvLookup) (domain.RuntimeConfig, error) {
	return BuildWithEnv(path, Overrides{}, lookup)
}

func Build(path string, overrides Overrides) (domain.RuntimeConfig, error) {
	return BuildWithEnv(path, overrides, os.LookupEnv)
}

func BuildWithEnv(path string, overrides Overrides, lookup EnvLookup) (domain.RuntimeConfig, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	cfg := Defaults()
	if path != "" {
		fileCfg, err := readFile(path, lookup)
		if err != nil {
			return domain.RuntimeConfig{}, err
		}
		cfg = fileCfg
	}

	if overrides.RefreshInterval != nil {
		cfg.RefreshInterval = *overrides.RefreshInterval
	}
	if overrides.ScrapeTimeout != nil {
		cfg.ScrapeTimeout = *overrides.ScrapeTimeout
	}
	if overrides.CurrentWindow != nil {
		cfg.CurrentWindow = *overrides.CurrentWindow
	}
	if overrides.History != nil {
		cfg.History = *overrides.History
	}
	if overrides.Windows != nil {
		cfg.Windows = append([]time.Duration(nil), overrides.Windows...)
	}
	if overrides.NoColor != nil {
		cfg.NoColor = *overrides.NoColor
	}
	if overrides.Demo != nil {
		cfg.Demo = *overrides.Demo
	}
	for _, flag := range overrides.Endpoints {
		ep := domain.EndpointConfig{Name: flag.Name, URL: flag.URL}
		cfg.Endpoints = append(cfg.Endpoints, ep)
	}

	if err := NormalizeAndValidate(&cfg); err != nil {
		return domain.RuntimeConfig{}, err
	}
	return cfg, nil
}

func ParseEndpointFlag(value string) (EndpointFlag, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return EndpointFlag{}, errors.New("endpoint flag is empty")
	}
	if i := strings.IndexByte(value, '='); i > 0 {
		prefix := strings.TrimSpace(value[:i])
		rest := strings.TrimSpace(value[i+1:])
		if prefix != "" && !strings.Contains(prefix, "://") {
			if rest == "" {
				return EndpointFlag{}, fmt.Errorf("endpoint %q has empty URL", prefix)
			}
			return EndpointFlag{Name: prefix, URL: rest}, nil
		}
	}
	return EndpointFlag{URL: value}, nil
}

func NormalizeAndValidate(cfg *domain.RuntimeConfig) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.RefreshInterval <= 0 {
		return errors.New("refresh_interval must be positive")
	}
	if cfg.ScrapeTimeout <= 0 {
		return errors.New("scrape_timeout must be positive")
	}
	if cfg.CurrentWindow <= 0 {
		return errors.New("current_window must be positive")
	}
	if cfg.History <= 0 {
		return errors.New("history must be positive")
	}
	if cfg.History < 15*time.Minute {
		return errors.New("history must be at least 15m")
	}
	if cfg.CurrentWindow < cfg.RefreshInterval {
		return errors.New("current_window must be greater than or equal to refresh_interval")
	}
	if cfg.CurrentWindow > cfg.History {
		return errors.New("current_window must not exceed history")
	}
	for i, window := range cfg.Windows {
		if window <= 0 {
			return fmt.Errorf("windows[%d] must be positive", i)
		}
		if window > cfg.History {
			return fmt.Errorf("windows[%d] must not exceed history", i)
		}
	}

	seen := make(map[string]struct{}, len(cfg.Endpoints))
	for i := range cfg.Endpoints {
		ep, err := NormalizeEndpoint(cfg.Endpoints[i])
		if err != nil {
			return fmt.Errorf("endpoints[%d]: %w", i, err)
		}
		if _, ok := seen[ep.Name]; ok {
			return fmt.Errorf("endpoint name %q is not unique", ep.Name)
		}
		seen[ep.Name] = struct{}{}
		cfg.Endpoints[i] = ep
	}
	return nil
}

func NormalizeEndpoint(ep domain.EndpointConfig) (domain.EndpointConfig, error) {
	ep.Name = strings.TrimSpace(ep.Name)
	ep.URL = strings.TrimSpace(ep.URL)
	ep.MetricsPath = strings.TrimSpace(ep.MetricsPath)
	ep.Model = strings.TrimSpace(ep.Model)
	if ep.URL == "" {
		return domain.EndpointConfig{}, errors.New("url is required")
	}

	u, err := parseURL(ep.URL)
	if err != nil {
		return domain.EndpointConfig{}, err
	}
	if ep.Name == "" {
		ep.Name = defaultEndpointName(u)
	}
	if err := validateEndpointName(ep.Name); err != nil {
		return domain.EndpointConfig{}, err
	}
	if ep.MetricsPath != "" {
		if strings.ContainsAny(ep.MetricsPath, "\r\n") {
			return domain.EndpointConfig{}, errors.New("metrics_path must not contain newlines")
		}
		if strings.ContainsAny(ep.MetricsPath, "?#") {
			return domain.EndpointConfig{}, errors.New("metrics_path must be a URL path, not a query or fragment")
		}
		if !strings.HasPrefix(ep.MetricsPath, "/") {
			ep.MetricsPath = "/" + ep.MetricsPath
		}
		u.Path = cleanPath(ep.MetricsPath)
		u.RawPath = ""
		ep.MetricsPath = u.EscapedPath()
	} else if u.EscapedPath() == "" || u.EscapedPath() == "/" {
		ep.MetricsPath = "/metrics"
		u.Path = "/metrics"
	} else {
		ep.MetricsPath = u.EscapedPath()
	}

	headers, err := normalizeHeaders(ep.Headers)
	if err != nil {
		return domain.EndpointConfig{}, err
	}
	ep.Headers = headers
	ep.MetricsURL = u.String()
	ep.SanitizedURL = SanitizeURL(ep.MetricsURL)
	return ep, nil
}

func SanitizeURL(raw string) string {
	u, err := parseURL(raw)
	if err != nil {
		return "<invalid-url>"
	}
	if u.User != nil {
		u.User = url.User(redacted)
	}
	values := u.Query()
	for key, vals := range values {
		if isSecretName(key) {
			for i := range vals {
				vals[i] = redacted
			}
			values[key] = vals
		}
	}
	u.RawQuery = values.Encode()
	return u.String()
}

func RedactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if isSecretName(key) {
			out[key] = redacted
			continue
		}
		out[key] = value
	}
	return out
}

func readFile(path string, lookup EnvLookup) (domain.RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.RuntimeConfig{}, err
	}
	var raw rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if !errors.Is(err, io.EOF) {
			return domain.RuntimeConfig{}, err
		}
	}
	return decodeRaw(raw, lookup)
}

func decodeRaw(raw rawConfig, lookup EnvLookup) (domain.RuntimeConfig, error) {
	cfg := Defaults()
	var err error
	if raw.RefreshInterval != "" {
		cfg.RefreshInterval, err = parseDurationField(raw.RefreshInterval, "refresh_interval", lookup)
		if err != nil {
			return domain.RuntimeConfig{}, err
		}
	}
	if raw.ScrapeTimeout != "" {
		cfg.ScrapeTimeout, err = parseDurationField(raw.ScrapeTimeout, "scrape_timeout", lookup)
		if err != nil {
			return domain.RuntimeConfig{}, err
		}
	}
	if raw.CurrentWindow != "" {
		cfg.CurrentWindow, err = parseDurationField(raw.CurrentWindow, "current_window", lookup)
		if err != nil {
			return domain.RuntimeConfig{}, err
		}
	}
	if raw.History != "" {
		cfg.History, err = parseDurationField(raw.History, "history", lookup)
		if err != nil {
			return domain.RuntimeConfig{}, err
		}
	}
	if raw.Windows != nil {
		cfg.Windows = make([]time.Duration, 0, len(raw.Windows))
		for i, rawWindow := range raw.Windows {
			window, err := parseDurationField(rawWindow, fmt.Sprintf("windows[%d]", i), lookup)
			if err != nil {
				return domain.RuntimeConfig{}, err
			}
			cfg.Windows = append(cfg.Windows, window)
		}
	}
	if raw.NoColor != nil {
		cfg.NoColor = *raw.NoColor
	}
	if raw.Demo != nil {
		cfg.Demo = *raw.Demo
	}
	for i, rawEP := range raw.Endpoints {
		ep, err := decodeEndpoint(rawEP, i, lookup)
		if err != nil {
			return domain.RuntimeConfig{}, err
		}
		cfg.Endpoints = append(cfg.Endpoints, ep)
	}
	return cfg, nil
}

func decodeEndpoint(raw rawEndpoint, index int, lookup EnvLookup) (domain.EndpointConfig, error) {
	field := func(name string) string {
		return fmt.Sprintf("endpoints[%d].%s", index, name)
	}
	name, err := expandField(raw.Name, field("name"), lookup)
	if err != nil {
		return domain.EndpointConfig{}, err
	}
	rawURL, err := expandField(raw.URL, field("url"), lookup)
	if err != nil {
		return domain.EndpointConfig{}, err
	}
	metricsPath, err := expandField(raw.MetricsPath, field("metrics_path"), lookup)
	if err != nil {
		return domain.EndpointConfig{}, err
	}
	model, err := expandField(raw.Model, field("model"), lookup)
	if err != nil {
		return domain.EndpointConfig{}, err
	}
	headers := make(map[string]string, len(raw.Headers))
	for key, value := range raw.Headers {
		expandedKey, err := expandField(key, field("headers."+key), lookup)
		if err != nil {
			return domain.EndpointConfig{}, err
		}
		expandedValue, err := expandField(value, field("headers."+key), lookup)
		if err != nil {
			return domain.EndpointConfig{}, err
		}
		headers[expandedKey] = expandedValue
	}
	return domain.EndpointConfig{
		Name:        name,
		URL:         rawURL,
		MetricsPath: metricsPath,
		Model:       model,
		Headers:     headers,
	}, nil
}

func parseDurationField(raw, field string, lookup EnvLookup) (time.Duration, error) {
	expanded, err := expandField(raw, field, lookup)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", field, err)
	}
	return d, nil
}

func expandField(value, field string, lookup EnvLookup) (string, error) {
	var missing []string
	expanded := os.Expand(value, func(name string) string {
		if v, ok := lookup(name); ok {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("%s references missing environment variable %s", field, strings.Join(missing, ", "))
	}
	return expanded, nil
}

func parseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("url is required")
	}
	if strings.ContainsAny(raw, "\r\n") {
		return nil, errors.New("url must not contain newlines")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("url host is required")
	}
	return u, nil
}

func defaultEndpointName(u *url.URL) string {
	name := u.Host
	if name == "" {
		name = "endpoint"
	}
	return name
}

func validateEndpointName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	if strings.ContainsAny(name, "\r\n") {
		return errors.New("name must not contain newlines")
	}
	return nil
}

func cleanPath(path string) string {
	if path == "" {
		return "/"
	}
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return "/" + strings.Join(out, "/")
}

func normalizeHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		key = http.CanonicalHeaderKey(strings.TrimSpace(key))
		if key == "" {
			return nil, errors.New("header name is required")
		}
		if strings.ContainsAny(key, ":\r\n") {
			return nil, fmt.Errorf("header %q is invalid", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("header %q value must not contain newlines", key)
		}
		out[key] = value
	}
	return out, nil
}

func isSecretName(name string) bool {
	n := strings.ToLower(name)
	secretParts := []string{"authorization", "auth", "token", "secret", "password", "passwd", "api-key", "apikey", "key", "credential", "signature", "sig"}
	for _, part := range secretParts {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}
