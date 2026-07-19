package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/pflag"

	"gpttop/internal/aggregate"
	"gpttop/internal/buildinfo"
	"gpttop/internal/config"
	"gpttop/internal/domain"
	"gpttop/internal/history"
	"gpttop/internal/output"
	"gpttop/internal/prom"
	"gpttop/internal/scrape"
	"gpttop/internal/ui"
)

type cliOptions struct {
	configPath     string
	endpoints      config.EndpointFlags
	interval       time.Duration
	timeout        time.Duration
	currentWindow  time.Duration
	history        time.Duration
	once           bool
	sampleDuration time.Duration
	output         string
	demo           bool
	noColor        bool
	version        bool
	changed        map[string]bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stdout)
	if err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if opts.version {
		fmt.Fprintln(stdout, buildinfo.String())
		return 0
	}

	cfg, err := buildConfig(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg = withDemoEndpoints(cfg)
	if !cfg.Demo && len(cfg.Endpoints) == 0 {
		fmt.Fprintln(stderr, "no endpoints configured; pass --endpoint, --config, or --demo")
		return 2
	}

	if opts.once || !isTerminal(os.Stdout) {
		code, err := runOnce(cfg, opts, stdout, stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			if code == 0 {
				code = 1
			}
		}
		return code
	}

	if err := runInteractive(cfg, opts, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func parseArgs(args []string, out io.Writer) (cliOptions, error) {
	opts := cliOptions{
		interval:       domain.DefaultInterval,
		timeout:        domain.DefaultTimeout,
		currentWindow:  domain.DefaultCurrentWindow,
		history:        domain.DefaultHistory,
		sampleDuration: domain.DefaultInterval,
		output:         "table",
		changed:        map[string]bool{},
	}
	fs := pflag.NewFlagSet("gpttop", pflag.ContinueOnError)
	fs.SetOutput(out)
	fs.SortFlags = false
	fs.VarP(&opts.endpoints, "endpoint", "e", "vLLM base or metrics URL, optionally NAME=URL; repeatable")
	fs.StringVarP(&opts.configPath, "config", "c", "", "YAML configuration file")
	fs.DurationVarP(&opts.interval, "interval", "i", opts.interval, "scrape interval")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "per-endpoint scrape timeout")
	fs.DurationVar(&opts.currentWindow, "current-window", opts.currentWindow, "window used for now rates")
	fs.DurationVar(&opts.history, "history", opts.history, "in-memory history retention")
	fs.BoolVar(&opts.once, "once", false, "take two scrapes, render output, and exit")
	fs.DurationVar(&opts.sampleDuration, "sample-duration", opts.sampleDuration, "delay between --once scrapes")
	fs.StringVar(&opts.output, "output", opts.output, "output format for --once: table or json")
	fs.BoolVar(&opts.demo, "demo", false, "run against deterministic synthetic data")
	fs.BoolVar(&opts.noColor, "no-color", false, "disable ANSI color")
	fs.BoolVar(&opts.version, "version", false, "print version, commit, and build date")
	fs.Usage = func() {
		fmt.Fprintln(out, "gpttop [flags]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Examples:")
		fmt.Fprintln(out, "  gpttop --endpoint http://127.0.0.1:8000")
		fmt.Fprintln(out, "  gpttop --endpoint local=http://127.0.0.1:8000 --endpoint gpu02=http://10.0.0.22:8000/metrics")
		fmt.Fprintln(out, "  gpttop --config ./gpttop.yaml")
		fmt.Fprintln(out, "  gpttop --once --output json --endpoint http://127.0.0.1:8000")
		fmt.Fprintln(out, "  gpttop --demo")
		fmt.Fprintln(out)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	fs.Visit(func(flag *pflag.Flag) {
		opts.changed[flag.Name] = true
	})
	switch opts.output {
	case "table", "json":
	default:
		return opts, fmt.Errorf("--output must be table or json, got %q", opts.output)
	}
	if opts.sampleDuration <= 0 {
		return opts, errors.New("--sample-duration must be positive")
	}
	return opts, nil
}

func buildConfig(opts cliOptions) (domain.RuntimeConfig, error) {
	overrides := config.Overrides{Endpoints: []config.EndpointFlag(opts.endpoints)}
	if opts.changed["interval"] {
		v := opts.interval
		overrides.RefreshInterval = &v
	}
	if opts.changed["timeout"] {
		v := opts.timeout
		overrides.ScrapeTimeout = &v
	}
	if opts.changed["current-window"] {
		v := opts.currentWindow
		overrides.CurrentWindow = &v
	}
	if opts.changed["history"] {
		v := opts.history
		overrides.History = &v
	}
	if opts.changed["no-color"] {
		v := opts.noColor
		overrides.NoColor = &v
	}
	if opts.changed["demo"] {
		v := opts.demo
		overrides.Demo = &v
	}
	cfg, err := config.Build(opts.configPath, overrides)
	if err != nil {
		return domain.RuntimeConfig{}, err
	}
	if cfg.NoColor {
		opts.noColor = true
	}
	return cfg, nil
}

func runOnce(cfg domain.RuntimeConfig, opts cliOptions, stdout, stderr io.Writer) (int, error) {
	store := history.NewStore(cfg.History)
	if cfg.Demo {
		addDemoScrape(store, cfg, time.Now().Add(-opts.sampleDuration), 0)
		addDemoScrape(store, cfg, time.Now(), 1)
	} else {
		s := newScraper(cfg)
		defer s.Shutdown()
		ctx := context.Background()
		scrapeAndStore(ctx, s, store, cfg.Endpoints, true)
		time.Sleep(opts.sampleDuration)
		scrapeAndStore(ctx, s, store, cfg.Endpoints, true)
	}
	snapshot := aggregate.BuildSnapshot(store, cfg, time.Now())
	if opts.output == "json" {
		if err := output.WriteJSON(stdout, snapshot); err != nil {
			return 1, err
		}
	} else {
		if err := output.WriteTable(stdout, snapshot); err != nil {
			return 1, err
		}
	}
	if !hasUsableSample(snapshot) {
		fmt.Fprintln(stderr, "no endpoint produced a usable sample")
		return 1, nil
	}
	return 0, nil
}

func runInteractive(cfg domain.RuntimeConfig, opts cliOptions, stderr io.Writer) error {
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	store := history.NewStore(cfg.History)
	provider := aggregate.Provider{Store: store, Config: cfg}
	refreshCh := make(chan struct{}, 1)
	uiOpts := ui.Options{
		NoColor: opts.noColor || cfg.NoColor,
		Refresh: func() tea.Cmd {
			return func() tea.Msg {
				select {
				case refreshCh <- struct{}{}:
				default:
				}
				return ui.RefreshRequestedMsg{At: time.Now()}
			}
		},
	}
	program := ui.NewProgram(provider, uiOpts, tea.WithAltScreen())

	var wg sync.WaitGroup
	if cfg.Demo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runDemoLoop(ctx, store, cfg, program)
		}()
	} else {
		s := newScraper(cfg)
		defer s.Shutdown()
		wg.Add(1)
		go func() {
			defer wg.Done()
			runScrapeLoop(ctx, s, store, cfg, program, refreshCh)
		}()
	}
	go func() {
		<-ctx.Done()
		program.Quit()
	}()

	_, err := program.Run()
	cancel()
	wg.Wait()
	return err
}

func runScrapeLoop(ctx context.Context, s *scrape.Scraper, store *history.Store, cfg domain.RuntimeConfig, program *tea.Program, refreshCh <-chan struct{}) {
	publish := func() {
		program.Send(ui.UpdateMsg{Snapshot: aggregate.BuildSnapshot(store, cfg, time.Now())})
	}
	scrapeAndStoreStreaming(ctx, s, store, cfg.Endpoints, true, publish)
	ticker := time.NewTicker(cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scrapeAndStoreStreaming(ctx, s, store, cfg.Endpoints, false, publish)
		case <-refreshCh:
			scrapeAndStoreStreaming(ctx, s, store, cfg.Endpoints, true, publish)
		}
	}
}

func runDemoLoop(ctx context.Context, store *history.Store, cfg domain.RuntimeConfig, program *tea.Program) {
	ticker := time.NewTicker(cfg.RefreshInterval)
	defer ticker.Stop()
	step := 0
	addDemoScrape(store, cfg, time.Now(), step)
	program.Send(ui.UpdateMsg{Snapshot: aggregate.BuildSnapshot(store, cfg, time.Now())})
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			step++
			addDemoScrape(store, cfg, time.Now(), step)
			program.Send(ui.UpdateMsg{Snapshot: aggregate.BuildSnapshot(store, cfg, time.Now())})
		}
	}
}

func newScraper(cfg domain.RuntimeConfig) *scrape.Scraper {
	parser := scrape.ParserFunc(func(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error) {
		return prom.Parse(bytes.NewReader(body), endpoint.Name, endpoint.URL, endpoint.MetricsURL, at)
	})
	return scrape.New(parser, scrape.WithTimeout(cfg.ScrapeTimeout))
}

func scrapeAndStore(ctx context.Context, s *scrape.Scraper, store *history.Store, endpoints []domain.EndpointConfig, force bool) {
	updates := s.ScrapeAll(ctx, endpoints, force)
	for _, update := range updates {
		store.AddStatus(update.Status)
		if update.Sample != nil && update.Status.Success {
			store.AddSample(update.Sample)
		}
	}
}

func scrapeAndStoreStreaming(ctx context.Context, s *scrape.Scraper, store *history.Store, endpoints []domain.EndpointConfig, force bool, publish func()) {
	var wg sync.WaitGroup
	updates := make(chan domain.EndpointUpdate, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint := endpoint
		wg.Add(1)
		go func() {
			defer wg.Done()
			updates <- s.ScrapeEndpoint(ctx, endpoint, force)
		}()
	}
	go func() {
		wg.Wait()
		close(updates)
	}()
	for update := range updates {
		store.AddStatus(update.Status)
		if update.Sample != nil && update.Status.Success {
			store.AddSample(update.Sample)
		}
		if publish != nil {
			publish()
		}
	}
}

func withDemoEndpoints(cfg domain.RuntimeConfig) domain.RuntimeConfig {
	if !cfg.Demo || len(cfg.Endpoints) > 0 {
		return cfg
	}
	demos := []domain.EndpointConfig{
		{Name: "demo-healthy", URL: "http://demo-healthy.local:8000"},
		{Name: "demo-pressure", URL: "http://demo-pressure.local:8000"},
	}
	cfg.Endpoints = make([]domain.EndpointConfig, 0, len(demos))
	for _, ep := range demos {
		normalized, err := config.NormalizeEndpoint(ep)
		if err == nil {
			cfg.Endpoints = append(cfg.Endpoints, normalized)
		}
	}
	return cfg
}

func hasUsableSample(snapshot domain.AppSnapshot) bool {
	for _, row := range snapshot.Rows {
		if row.State == domain.StateUP || row.State == domain.StateWarming || row.State == domain.StateStale {
			for _, metric := range row.Metrics {
				if metric.Now.Available || metric.OneMin.Available || metric.Five.Available || metric.Fifteen.Available {
					return true
				}
			}
		}
	}
	return false
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func conciseError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 240 {
		msg = msg[:237] + "..."
	}
	return msg
}
