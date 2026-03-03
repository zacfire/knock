package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const appName = "knock"

type Config struct {
	DefaultProvider string             `json:"default_provider"`
	ActiveProfile   string             `json:"active_profile"`
	Providers       ProviderCollection `json:"providers"`
	Profiles        map[string]Profile `json:"profiles"`
}

type ProviderCollection struct {
	Telegram TelegramProvider `json:"telegram"`
	Bark     BarkProvider     `json:"bark"`
}

type TelegramProvider struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type BarkProvider struct {
	Enabled   bool   `json:"enabled"`
	ServerURL string `json:"server_url"`
	DeviceKey string `json:"device_key"`
}

type Profile struct {
	Name  string `json:"name"`
	Rules []Rule `json:"rules"`
}

type Rule struct {
	Name            string `json:"name"`
	Pattern         string `json:"pattern"`
	Event           string `json:"event"`
	IdleSeconds     int    `json:"idle_seconds"`
	CooldownSeconds int    `json:"cooldown_seconds"`
	Severity        string `json:"severity"`
}

type compiledRule struct {
	Rule
	Regex *regexp.Regexp
}

type notification struct {
	Title    string
	Body     string
	Severity string
}

type pendingAlert struct {
	Rule      Rule
	MatchedAt time.Time
	Line      string
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "provider":
		err = cmdProvider(os.Args[2:])
	case "send":
		err = cmdSend(os.Args[2:])
	case "test":
		err = cmdTest(os.Args[2:])
	case "profile":
		err = cmdProfile(os.Args[2:])
	case "watch":
		err = cmdWatch(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`knock - agent notification CLI

Usage:
  knock init [--provider telegram|bark] [provider options]
  knock provider add telegram --token <token> --chat-id <id>
  knock provider add bark --key <device-key> [--server https://api.day.app]
  knock send [--provider <name>] [--title <title>] [--severity info|high] <message>
  knock test [--provider <name>]
  knock profile use <claude|codex|gemini>
  knock profile list
  knock watch [--profile <name>] [--provider <name>] [--debug] -- <agent command>
  knock doctor
`) 
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	provider := fs.String("provider", "", "default provider: telegram or bark")
	token := fs.String("token", "", "telegram bot token")
	chatID := fs.String("chat-id", "", "telegram chat id")
	barkServer := fs.String("server", "https://api.day.app", "bark server url")
	barkKey := fs.String("key", "", "bark device key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := defaultConfig()
	if err := configureProviderFromFlags(&cfg, *provider, *token, *chatID, *barkServer, *barkKey); err != nil {
		return err
	}

	path, err := saveConfig(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Initialized config: %s\n", path)
	return nil
}

func cmdProvider(args []string) error {
	if len(args) < 1 || args[0] != "add" {
		return errors.New("usage: knock provider add <telegram|bark> [flags]")
	}
	if len(args) < 2 {
		return errors.New("usage: knock provider add <telegram|bark> [flags]")
	}

	providerName := args[1]
	cfg, err := loadOrDefaultConfig()
	if err != nil {
		return err
	}

	switch providerName {
	case "telegram":
		fs := flag.NewFlagSet("provider add telegram", flag.ContinueOnError)
		token := fs.String("token", "", "telegram bot token")
		chatID := fs.String("chat-id", "", "telegram chat id")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *token == "" || *chatID == "" {
			return errors.New("telegram requires --token and --chat-id")
		}
		cfg.Providers.Telegram = TelegramProvider{Enabled: true, BotToken: *token, ChatID: *chatID}
	case "bark":
		fs := flag.NewFlagSet("provider add bark", flag.ContinueOnError)
		server := fs.String("server", "https://api.day.app", "bark server url")
		key := fs.String("key", "", "bark device key")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *key == "" {
			return errors.New("bark requires --key")
		}
		cfg.Providers.Bark = BarkProvider{Enabled: true, ServerURL: strings.TrimRight(*server, "/"), DeviceKey: *key}
	default:
		return fmt.Errorf("unsupported provider: %s", providerName)
	}

	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = providerName
	}

	path, err := saveConfig(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Provider %s configured in %s\n", providerName, path)
	return nil
}

func cmdSend(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	provider := fs.String("provider", "", "provider override")
	title := fs.String("title", "knock", "notification title")
	severity := fs.String("severity", "info", "severity label")
	if err := fs.Parse(args); err != nil {
		return err
	}

	message := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if message == "" {
		return errors.New("usage: knock send [flags] <message>")
	}

	targetProvider := strings.TrimSpace(*provider)
	if targetProvider == "" {
		targetProvider = cfg.DefaultProvider
	}
	if targetProvider == "" {
		return errors.New("no default provider configured; run knock provider add ...")
	}

	if err := sendNotification(cfg, targetProvider, notification{Title: *title, Body: message, Severity: *severity}); err != nil {
		return err
	}
	fmt.Printf("Notification sent via %s\n", targetProvider)
	return nil
}

func cmdTest(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	provider := fs.String("provider", "", "provider override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetProvider := strings.TrimSpace(*provider)
	if targetProvider == "" {
		targetProvider = cfg.DefaultProvider
	}
	if targetProvider == "" {
		return errors.New("no default provider configured; run knock provider add ...")
	}

	body := fmt.Sprintf("knock test at %s", time.Now().Format(time.RFC3339))
	if err := sendNotification(cfg, targetProvider, notification{Title: "knock test", Body: body, Severity: "info"}); err != nil {
		return err
	}
	fmt.Printf("Test notification sent via %s\n", targetProvider)
	return nil
}

func cmdProfile(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: knock profile <use|list>")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	switch args[0] {
	case "use":
		if len(args) < 2 {
			return errors.New("usage: knock profile use <name>")
		}
		name := args[1]
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("profile not found: %s", name)
		}
		cfg.ActiveProfile = name
		path, err := saveConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Active profile set to %s (%s)\n", name, path)
		return nil
	case "list":
		names := make([]string, 0, len(cfg.Profiles))
		for k := range cfg.Profiles {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			marker := " "
			if name == cfg.ActiveProfile {
				marker = "*"
			}
			fmt.Printf("%s %s (%d rules)\n", marker, name, len(cfg.Profiles[name].Rules))
		}
		return nil
	default:
		return errors.New("usage: knock profile <use|list>")
	}
}

func cmdWatch(args []string) error {
	sep := -1
	for i, v := range args {
		if v == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || sep == len(args)-1 {
		return errors.New("usage: knock watch [flags] -- <command>")
	}

	watchArgs := args[:sep]
	cmdArgs := args[sep+1:]

	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	providerOverride := fs.String("provider", "", "provider override")
	profileOverride := fs.String("profile", "", "profile override")
	debug := fs.Bool("debug", false, "print matcher internals")
	if err := fs.Parse(watchArgs); err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	targetProvider := strings.TrimSpace(*providerOverride)
	if targetProvider == "" {
		targetProvider = cfg.DefaultProvider
	}
	if targetProvider == "" {
		return errors.New("no provider configured")
	}

	profileName := cfg.ActiveProfile
	if strings.TrimSpace(*profileOverride) != "" {
		profileName = strings.TrimSpace(*profileOverride)
	}
	profile, ok := cfg.Profiles[profileName]
	if !ok {
		return fmt.Errorf("profile not found: %s", profileName)
	}

	rules, err := compileRules(profile.Rules)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	child := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	stdoutPipe, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := child.StderrPipe()
	if err != nil {
		return err
	}
	stdinPipe, err := child.StdinPipe()
	if err != nil {
		return err
	}

	if err := child.Start(); err != nil {
		return err
	}

	lineCh := make(chan string, 128)
	inputCh := make(chan struct{}, 16)
	pipeErrCh := make(chan error, 2)
	waitCh := make(chan error, 1)

	go streamLines(stdoutPipe, lineCh, pipeErrCh)
	go streamLines(stderrPipe, lineCh, pipeErrCh)
	go proxyInput(os.Stdin, stdinPipe, inputCh)
	go func() { waitCh <- child.Wait() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	fmt.Printf("watching %q with profile=%s provider=%s\n", strings.Join(cmdArgs, " "), profileName, targetProvider)

	lastInput := time.Now()
	cooldownUntil := map[string]time.Time{}
	var pending *pendingAlert
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-inputCh:
			lastInput = time.Now()
			pending = nil
		case line := <-lineCh:
			fmt.Println(line)
			now := time.Now()
			for _, r := range rules {
				if !r.Regex.MatchString(line) {
					continue
				}
				if until, ok := cooldownUntil[r.Name]; ok && now.Before(until) {
					if *debug {
						fmt.Printf("[knock-debug] rule=%s suppressed by cooldown\n", r.Name)
					}
					continue
				}
				if *debug {
					fmt.Printf("[knock-debug] rule=%s matched line=%q\n", r.Name, line)
				}
				if r.IdleSeconds <= 0 {
					msg := fmt.Sprintf("%s: %s", r.Event, line)
					if err := sendNotification(cfg, targetProvider, notification{Title: "knock", Body: msg, Severity: r.Severity}); err != nil {
						fmt.Fprintf(os.Stderr, "[knock] notify failed: %v\n", err)
					} else {
						cooldownUntil[r.Name] = now.Add(time.Duration(r.CooldownSeconds) * time.Second)
					}
					continue
				}
				pending = &pendingAlert{Rule: r.Rule, MatchedAt: now, Line: line}
			}
		case <-ticker.C:
			if pending == nil {
				continue
			}
			idleFor := time.Duration(pending.Rule.IdleSeconds) * time.Second
			if time.Since(pending.MatchedAt) < idleFor {
				continue
			}
			if time.Since(lastInput) < idleFor {
				continue
			}
			now := time.Now()
			if until, ok := cooldownUntil[pending.Rule.Name]; ok && now.Before(until) {
				pending = nil
				continue
			}
			msg := fmt.Sprintf("%s: no input for %ds after %q", pending.Rule.Event, pending.Rule.IdleSeconds, pending.Line)
			if err := sendNotification(cfg, targetProvider, notification{Title: "knock", Body: msg, Severity: pending.Rule.Severity}); err != nil {
				fmt.Fprintf(os.Stderr, "[knock] notify failed: %v\n", err)
			} else {
				cooldownUntil[pending.Rule.Name] = now.Add(time.Duration(pending.Rule.CooldownSeconds) * time.Second)
			}
			pending = nil
		case err := <-pipeErrCh:
			if err != nil && !errors.Is(err, io.EOF) {
				fmt.Fprintf(os.Stderr, "[knock] stream error: %v\n", err)
			}
		case sig := <-sigCh:
			if child.Process != nil {
				_ = child.Process.Signal(sig)
			}
		case err := <-waitCh:
			if err == nil {
				return nil
			}
			return err
		}
	}
}

func cmdDoctor(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: knock doctor")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	fmt.Println("Config check")
	fmt.Printf("- default provider: %s\n", valueOr(cfg.DefaultProvider, "(not set)"))
	fmt.Printf("- active profile: %s\n", valueOr(cfg.ActiveProfile, "(not set)"))

	if cfg.Providers.Telegram.Enabled {
		fmt.Println("- telegram: enabled")
	} else {
		fmt.Println("- telegram: disabled")
	}
	if cfg.Providers.Bark.Enabled {
		fmt.Println("- bark: enabled")
	} else {
		fmt.Println("- bark: disabled")
	}

	if cfg.DefaultProvider != "" {
		if err := validateProvider(cfg, cfg.DefaultProvider); err != nil {
			fmt.Printf("- provider validation: failed (%v)\n", err)
		} else {
			fmt.Println("- provider validation: ok")
		}
	}

	if _, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
		fmt.Println("- profile validation: ok")
	} else {
		fmt.Println("- profile validation: failed (active profile missing)")
	}
	return nil
}

func configureProviderFromFlags(cfg *Config, provider, token, chatID, barkServer, barkKey string) error {
	switch provider {
	case "":
		return nil
	case "telegram":
		if token == "" || chatID == "" {
			return errors.New("telegram init requires --token and --chat-id")
		}
		cfg.Providers.Telegram = TelegramProvider{Enabled: true, BotToken: token, ChatID: chatID}
		cfg.DefaultProvider = "telegram"
	case "bark":
		if barkKey == "" {
			return errors.New("bark init requires --key")
		}
		cfg.Providers.Bark = BarkProvider{Enabled: true, ServerURL: strings.TrimRight(barkServer, "/"), DeviceKey: barkKey}
		cfg.DefaultProvider = "bark"
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
	return nil
}

func defaultConfig() Config {
	profiles := defaultProfiles()
	return Config{
		DefaultProvider: "",
		ActiveProfile:   "claude",
		Providers:       ProviderCollection{},
		Profiles:        profiles,
	}
}

func defaultProfiles() map[string]Profile {
	return map[string]Profile{
		"claude": {
			Name: "claude",
			Rules: []Rule{
				{Name: "approval", Pattern: `(?i)allow\?.*\[y/?n\]`, Event: "Approval required", IdleSeconds: 30, CooldownSeconds: 120, Severity: "high"},
				{Name: "task_complete", Pattern: `(?i)(task|plan)\\s+complete`, Event: "Task completed", IdleSeconds: 0, CooldownSeconds: 60, Severity: "info"},
				{Name: "error", Pattern: `(?i)(error|failed|exception)`, Event: "Error detected", IdleSeconds: 15, CooldownSeconds: 60, Severity: "high"},
			},
		},
		"codex": {
			Name: "codex",
			Rules: []Rule{
				{Name: "approval", Pattern: `(?i)(confirm|approve|allow).*(y/n|\[y/?n\])`, Event: "Approval required", IdleSeconds: 30, CooldownSeconds: 120, Severity: "high"},
				{Name: "task_complete", Pattern: `(?i)(done|complete|finished)`, Event: "Task completed", IdleSeconds: 0, CooldownSeconds: 60, Severity: "info"},
				{Name: "error", Pattern: `(?i)(error|failed|exception)`, Event: "Error detected", IdleSeconds: 15, CooldownSeconds: 60, Severity: "high"},
			},
		},
		"gemini": {
			Name: "gemini",
			Rules: []Rule{
				{Name: "approval", Pattern: `(?i)(allow|confirm|continue).*(y/n|\[y/?n\])`, Event: "Approval required", IdleSeconds: 30, CooldownSeconds: 120, Severity: "high"},
				{Name: "task_complete", Pattern: `(?i)(complete|finished|all set)`, Event: "Task completed", IdleSeconds: 0, CooldownSeconds: 60, Severity: "info"},
				{Name: "error", Pattern: `(?i)(error|failed|exception)`, Event: "Error detected", IdleSeconds: 15, CooldownSeconds: 60, Severity: "high"},
			},
		},
	}
}

func compileRules(rules []Rule) ([]compiledRule, error) {
	result := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("rule %s has invalid regex: %w", r.Name, err)
		}
		result = append(result, compiledRule{Rule: r, Regex: re})
	}
	return result, nil
}

func streamLines(r io.Reader, out chan<- string, errCh chan<- error) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		out <- scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		errCh <- err
	}
}

func proxyInput(src io.Reader, dst io.WriteCloser, inputCh chan<- struct{}) {
	defer dst.Close()
	buf := make([]byte, 1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			select {
			case inputCh <- struct{}{}:
			default:
			}
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func sendNotification(cfg Config, provider string, n notification) error {
	if err := validateProvider(cfg, provider); err != nil {
		return err
	}

	switch provider {
	case "telegram":
		return sendTelegram(cfg.Providers.Telegram, n)
	case "bark":
		return sendBark(cfg.Providers.Bark, n)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
}

func validateProvider(cfg Config, provider string) error {
	switch provider {
	case "telegram":
		p := cfg.Providers.Telegram
		if !p.Enabled || p.BotToken == "" || p.ChatID == "" {
			return errors.New("telegram provider is not fully configured")
		}
		return nil
	case "bark":
		p := cfg.Providers.Bark
		if !p.Enabled || p.DeviceKey == "" || p.ServerURL == "" {
			return errors.New("bark provider is not fully configured")
		}
		return nil
	default:
		return fmt.Errorf("unknown provider: %s", provider)
	}
}

func sendTelegram(p TelegramProvider, n notification) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", p.BotToken)
	text := formatMessage(n)
	payload := url.Values{}
	payload.Set("chat_id", p.ChatID)
	payload.Set("text", text)
	payload.Set("disable_web_page_preview", "true")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.PostForm(endpoint, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("telegram status=%d body=%q", resp.StatusCode, string(body))
	}
	return nil
}

func sendBark(p BarkProvider, n notification) error {
	base := strings.TrimRight(p.ServerURL, "/")
	titleEsc := url.PathEscape(n.Title)
	bodyEsc := url.PathEscape(formatMessage(n))
	endpoint := fmt.Sprintf("%s/%s/%s/%s", base, p.DeviceKey, titleEsc, bodyEsc)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("bark status=%d body=%q", resp.StatusCode, string(body))
	}
	return nil
}

func formatMessage(n notification) string {
	severity := strings.ToUpper(strings.TrimSpace(n.Severity))
	if severity == "" {
		severity = "INFO"
	}
	return fmt.Sprintf("[%s] %s", severity, n.Body)
}

func configPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, appName, "config.json"), nil
}

func loadOrDefaultConfig() (Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultConfig(), nil
		}
		return Config{}, err
	}
	return cfg, nil
}

func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%w: config not found (%s). run: knock init", os.ErrNotExist, path)
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("failed to parse config: %w", err)
	}
	mergeMissingDefaults(&cfg)
	return cfg, nil
}

func saveConfig(cfg Config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	mergeMissingDefaults(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func mergeMissingDefaults(cfg *Config) {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	for name, profile := range defaultProfiles() {
		if _, ok := cfg.Profiles[name]; !ok {
			cfg.Profiles[name] = profile
		}
	}
	if cfg.ActiveProfile == "" {
		cfg.ActiveProfile = "claude"
	}
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
