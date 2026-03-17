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
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	appName           = "knock"
	appVersion        = "0.2.0"
	updateRepoURL     = "https://api.github.com/repos/zacfire/knock/releases/latest"
	updateCheckPeriod = 24 * time.Hour
)

type Config struct {
	DefaultProvider string             `json:"default_provider"`
	ActiveProfile   string             `json:"active_profile"`
	Providers       ProviderCollection `json:"providers"`
	Profiles        map[string]Profile `json:"profiles"`
	Metadata        Metadata           `json:"metadata,omitempty"`
}

type Metadata struct {
	Update UpdateMetadata `json:"update,omitempty"`
}

type UpdateMetadata struct {
	LastCheckedAt string `json:"last_checked_at,omitempty"`
	LastNoticedAt string `json:"last_noticed_at,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
}

type ProviderCollection struct {
	Telegram TelegramProvider `json:"telegram"`
	Bark     BarkProvider     `json:"bark"`
	Webhook  WebhookProvider  `json:"webhook"`
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

type WebhookProvider struct {
	Enabled      bool   `json:"enabled"`
	URL          string `json:"url"`
	Method       string `json:"method"`
	AuthHeader   string `json:"auth_header"`
	AuthValue    string `json:"auth_value"`
	TimeoutMilli int    `json:"timeout_milli"`
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

	maybeRunPassiveUpdateReminder(os.Args[1])

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
	case "rule":
		err = cmdRule(os.Args[2:])
	case "update":
		err = cmdUpdate(os.Args[2:])
	case "listen":
		err = cmdListen(os.Args[2:])
	case "watch":
		err = cmdWatch(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("knock %s\n", appVersion)
		return
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
  knock init [--provider telegram|bark|webhook] [provider options]
  knock provider add telegram --token <token> --chat-id <id>
  knock provider add bark --key <device-key> [--server https://api.day.app]
  knock provider add webhook --url <url> [--method POST] [--auth-header Authorization] [--auth-value 'Bearer ...']
  knock provider use <telegram|bark|webhook>
  knock provider list
  knock send [--provider <name>] [--title <title>] [--severity info|high] <message>
  knock test [--provider <name>]
  knock profile use <claude|codex|gemini>
  knock profile list
  knock rule list [--profile <name>]
  knock rule add --name <rule-name> --pattern <regex> [--event <text>] [--idle <sec>] [--cooldown <sec>] [--severity info|high] [--profile <name>]
  knock rule update --name <rule-name> [--new-name <name>] [--pattern <regex>] [--event <text>] [--idle <sec>] [--cooldown <sec>] [--severity info|high] [--profile <name>]
  knock rule remove --name <rule-name> [--profile <name>]
  knock update check [--quiet]
  knock listen [--port 9090] [--provider <name>] [--token <bearer-token>]
  knock watch [--profile <name>] [--provider <name>] [--debug] -- <agent command>
  knock doctor
  knock version
`) 
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	provider := fs.String("provider", "", "default provider: telegram, bark, or webhook")
	token := fs.String("token", "", "telegram bot token")
	chatID := fs.String("chat-id", "", "telegram chat id")
	barkServer := fs.String("server", "https://api.day.app", "bark server url")
	barkKey := fs.String("key", "", "bark device key")
	webhookURL := fs.String("url", "", "webhook endpoint url")
	webhookMethod := fs.String("method", "POST", "webhook method")
	authHeader := fs.String("auth-header", "", "webhook auth header name")
	authValue := fs.String("auth-value", "", "webhook auth header value")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := defaultConfig()
	if err := configureProviderFromFlags(&cfg, *provider, *token, *chatID, *barkServer, *barkKey, *webhookURL, *webhookMethod, *authHeader, *authValue); err != nil {
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
	if len(args) < 1 {
		return errors.New("usage: knock provider <add|use|list> ...")
	}

	cfg, err := loadOrDefaultConfig()
	if err != nil {
		return err
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errors.New("usage: knock provider add <telegram|bark|webhook> [flags]")
		}
		providerName := args[1]
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
		case "webhook":
			fs := flag.NewFlagSet("provider add webhook", flag.ContinueOnError)
			endpoint := fs.String("url", "", "webhook endpoint url")
			method := fs.String("method", "POST", "webhook method")
			authHeader := fs.String("auth-header", "", "auth header name")
			authValue := fs.String("auth-value", "", "auth header value")
			timeout := fs.Int("timeout-ms", 8000, "request timeout in milliseconds")
			if err := fs.Parse(args[2:]); err != nil {
				return err
			}
			if strings.TrimSpace(*endpoint) == "" {
				return errors.New("webhook requires --url")
			}
			if *timeout <= 0 {
				return errors.New("timeout-ms must be > 0")
			}
			cfg.Providers.Webhook = WebhookProvider{
				Enabled:      true,
				URL:          strings.TrimSpace(*endpoint),
				Method:       strings.ToUpper(strings.TrimSpace(*method)),
				AuthHeader:   strings.TrimSpace(*authHeader),
				AuthValue:    strings.TrimSpace(*authValue),
				TimeoutMilli: *timeout,
			}
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
	case "use":
		if len(args) < 2 {
			return errors.New("usage: knock provider use <telegram|bark|webhook>")
		}
		providerName := strings.TrimSpace(args[1])
		if err := validateProvider(cfg, providerName); err != nil {
			return err
		}
		cfg.DefaultProvider = providerName
		path, err := saveConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Default provider set to %s (%s)\n", providerName, path)
		return nil
	case "list":
		defaultMarker := func(name string) string {
			if name == cfg.DefaultProvider {
				return "*"
			}
			return " "
		}
		telegramStatus := "disabled"
		if cfg.Providers.Telegram.Enabled {
			telegramStatus = "enabled"
		}
		barkStatus := "disabled"
		if cfg.Providers.Bark.Enabled {
			barkStatus = "enabled"
		}
		webhookStatus := "disabled"
		if cfg.Providers.Webhook.Enabled {
			webhookStatus = "enabled"
		}

		fmt.Printf("%s telegram (%s)\n", defaultMarker("telegram"), telegramStatus)
		fmt.Printf("%s bark (%s)\n", defaultMarker("bark"), barkStatus)
		fmt.Printf("%s webhook (%s)\n", defaultMarker("webhook"), webhookStatus)
		return nil
	default:
		return errors.New("usage: knock provider <add|use|list> ...")
	}
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

func cmdRule(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: knock rule <list|add|update|remove>")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("rule list", flag.ContinueOnError)
		profileName := fs.String("profile", "", "profile override")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		targetProfile := strings.TrimSpace(*profileName)
		if targetProfile == "" {
			targetProfile = cfg.ActiveProfile
		}
		profile, ok := cfg.Profiles[targetProfile]
		if !ok {
			return fmt.Errorf("profile not found: %s", targetProfile)
		}
		if len(profile.Rules) == 0 {
			fmt.Printf("profile %s has no rules\n", targetProfile)
			return nil
		}
		for i, r := range profile.Rules {
			fmt.Printf("%d. %s pattern=%q event=%q idle=%ds cooldown=%ds severity=%s\n", i+1, r.Name, r.Pattern, r.Event, r.IdleSeconds, r.CooldownSeconds, r.Severity)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("rule add", flag.ContinueOnError)
		profileName := fs.String("profile", "", "profile override")
		name := fs.String("name", "", "rule name")
		pattern := fs.String("pattern", "", "regex pattern")
		event := fs.String("event", "Custom event", "event label")
		idle := fs.Int("idle", 30, "idle seconds before alert")
		cooldown := fs.Int("cooldown", 60, "cooldown seconds")
		severity := fs.String("severity", "info", "severity info|high")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" || strings.TrimSpace(*pattern) == "" {
			return errors.New("rule add requires --name and --pattern")
		}
		if *idle < 0 || *cooldown < 0 {
			return errors.New("idle and cooldown must be >= 0")
		}
		if _, err := regexp.Compile(*pattern); err != nil {
			return fmt.Errorf("invalid regex pattern: %w", err)
		}
		normalizedSeverity := strings.ToLower(strings.TrimSpace(*severity))
		if normalizedSeverity != "info" && normalizedSeverity != "high" {
			return errors.New("severity must be info or high")
		}

		targetProfile := strings.TrimSpace(*profileName)
		if targetProfile == "" {
			targetProfile = cfg.ActiveProfile
		}
		profile, ok := cfg.Profiles[targetProfile]
		if !ok {
			return fmt.Errorf("profile not found: %s", targetProfile)
		}
		for _, r := range profile.Rules {
			if r.Name == *name {
				return fmt.Errorf("rule already exists in profile %s: %s", targetProfile, *name)
			}
		}

		profile.Rules = append(profile.Rules, Rule{
			Name:            *name,
			Pattern:         *pattern,
			Event:           *event,
			IdleSeconds:     *idle,
			CooldownSeconds: *cooldown,
			Severity:        normalizedSeverity,
		})
		cfg.Profiles[targetProfile] = profile

		path, err := saveConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Rule %s added to profile %s (%s)\n", *name, targetProfile, path)
		return nil
	case "update":
		fs := flag.NewFlagSet("rule update", flag.ContinueOnError)
		profileName := fs.String("profile", "", "profile override")
		name := fs.String("name", "", "existing rule name")
		newName := fs.String("new-name", "", "new rule name")
		pattern := fs.String("pattern", "", "regex pattern")
		event := fs.String("event", "", "event label")
		idle := fs.Int("idle", -1, "idle seconds before alert")
		cooldown := fs.Int("cooldown", -1, "cooldown seconds")
		severity := fs.String("severity", "", "severity info|high")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" {
			return errors.New("rule update requires --name")
		}

		targetProfile := strings.TrimSpace(*profileName)
		if targetProfile == "" {
			targetProfile = cfg.ActiveProfile
		}
		profile, ok := cfg.Profiles[targetProfile]
		if !ok {
			return fmt.Errorf("profile not found: %s", targetProfile)
		}

		ruleIndex := -1
		for i, r := range profile.Rules {
			if r.Name == *name {
				ruleIndex = i
				break
			}
		}
		if ruleIndex < 0 {
			return fmt.Errorf("rule not found in profile %s: %s", targetProfile, *name)
		}

		if strings.TrimSpace(*newName) == "" && strings.TrimSpace(*pattern) == "" && strings.TrimSpace(*event) == "" && *idle < 0 && *cooldown < 0 && strings.TrimSpace(*severity) == "" {
			return errors.New("rule update requires at least one change flag")
		}

		updatedRule := profile.Rules[ruleIndex]
		if strings.TrimSpace(*newName) != "" {
			for i, r := range profile.Rules {
				if i != ruleIndex && r.Name == *newName {
					return fmt.Errorf("rule already exists in profile %s: %s", targetProfile, *newName)
				}
			}
			updatedRule.Name = strings.TrimSpace(*newName)
		}
		if strings.TrimSpace(*pattern) != "" {
			if _, err := regexp.Compile(*pattern); err != nil {
				return fmt.Errorf("invalid regex pattern: %w", err)
			}
			updatedRule.Pattern = *pattern
		}
		if strings.TrimSpace(*event) != "" {
			updatedRule.Event = *event
		}
		if *idle >= 0 {
			updatedRule.IdleSeconds = *idle
		}
		if *cooldown >= 0 {
			updatedRule.CooldownSeconds = *cooldown
		}
		if strings.TrimSpace(*severity) != "" {
			normalizedSeverity := strings.ToLower(strings.TrimSpace(*severity))
			if normalizedSeverity != "info" && normalizedSeverity != "high" {
				return errors.New("severity must be info or high")
			}
			updatedRule.Severity = normalizedSeverity
		}

		profile.Rules[ruleIndex] = updatedRule
		cfg.Profiles[targetProfile] = profile

		path, err := saveConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Rule %s updated in profile %s (%s)\n", *name, targetProfile, path)
		return nil
	case "remove":
		fs := flag.NewFlagSet("rule remove", flag.ContinueOnError)
		profileName := fs.String("profile", "", "profile override")
		name := fs.String("name", "", "rule name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" {
			return errors.New("rule remove requires --name")
		}
		targetProfile := strings.TrimSpace(*profileName)
		if targetProfile == "" {
			targetProfile = cfg.ActiveProfile
		}
		profile, ok := cfg.Profiles[targetProfile]
		if !ok {
			return fmt.Errorf("profile not found: %s", targetProfile)
		}

		nextRules := make([]Rule, 0, len(profile.Rules))
		removed := false
		for _, r := range profile.Rules {
			if r.Name == *name {
				removed = true
				continue
			}
			nextRules = append(nextRules, r)
		}
		if !removed {
			return fmt.Errorf("rule not found in profile %s: %s", targetProfile, *name)
		}
		profile.Rules = nextRules
		cfg.Profiles[targetProfile] = profile

		path, err := saveConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Rule %s removed from profile %s (%s)\n", *name, targetProfile, path)
		return nil
	default:
		return errors.New("usage: knock rule <list|add|update|remove>")
	}
}

type listenPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
}

func cmdListen(args []string) error {
	fs := flag.NewFlagSet("listen", flag.ContinueOnError)
	port := fs.Int("port", 9090, "listen port")
	providerOverride := fs.String("provider", "", "provider override")
	token := fs.String("token", "", "bearer token for authentication")
	if err := fs.Parse(args); err != nil {
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
	if err := validateProvider(cfg, targetProvider); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if *token != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+*token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		var p listenPayload
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&p); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(p.Body) == "" {
			http.Error(w, "body is required", http.StatusBadRequest)
			return
		}
		title := p.Title
		if title == "" {
			title = "knock"
		}
		severity := strings.ToLower(strings.TrimSpace(p.Severity))
		if severity == "" {
			severity = "info"
		}
		if err := sendNotification(cfg, targetProvider, notification{Title: title, Body: p.Body, Severity: severity}); err != nil {
			fmt.Fprintf(os.Stderr, "[knock] notify failed: %v\n", err)
			http.Error(w, "notification failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true}`)
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	fmt.Printf("listening on :%d (provider=%s, auth=%v)\n", *port, targetProvider, *token != "")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
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

	// Telegram bidirectional: start callback poller when provider is telegram
	telegramInteractive := targetProvider == "telegram" && cfg.Providers.Telegram.Enabled
	replyCh := make(chan string, 8)
	if telegramInteractive {
		go pollTelegramCallbacks(ctx, cfg.Providers.Telegram, replyCh)
	}

	// watchNotify sends a notification, using interactive mode for telegram+high severity
	watchNotify := func(n notification) error {
		if telegramInteractive && strings.ToLower(n.Severity) == "high" {
			return sendTelegramInteractive(cfg.Providers.Telegram, n)
		}
		return sendNotification(cfg, targetProvider, n)
	}

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
		case reply := <-replyCh:
			// Telegram button reply → write to child stdin
			lastInput = time.Now()
			pending = nil
			if _, err := fmt.Fprintln(stdinPipe, reply); err != nil {
				fmt.Fprintf(os.Stderr, "[knock] failed to write reply to stdin: %v\n", err)
			} else if *debug {
				fmt.Printf("[knock-debug] telegram reply=%q written to stdin\n", reply)
			}
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
					if err := watchNotify(notification{Title: "knock", Body: msg, Severity: r.Severity}); err != nil {
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
			if err := watchNotify(notification{Title: "knock", Body: msg, Severity: pending.Rule.Severity}); err != nil {
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
	if cfg.Providers.Webhook.Enabled {
		fmt.Println("- webhook: enabled")
	} else {
		fmt.Println("- webhook: disabled")
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

func configureProviderFromFlags(cfg *Config, provider, token, chatID, barkServer, barkKey, webhookURL, webhookMethod, webhookAuthHeader, webhookAuthValue string) error {
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
	case "webhook":
		if strings.TrimSpace(webhookURL) == "" {
			return errors.New("webhook init requires --url")
		}
		method := strings.ToUpper(strings.TrimSpace(webhookMethod))
		if method == "" {
			method = "POST"
		}
		cfg.Providers.Webhook = WebhookProvider{
			Enabled:      true,
			URL:          strings.TrimSpace(webhookURL),
			Method:       method,
			AuthHeader:   strings.TrimSpace(webhookAuthHeader),
			AuthValue:    strings.TrimSpace(webhookAuthValue),
			TimeoutMilli: 8000,
		}
		cfg.DefaultProvider = "webhook"
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
				{Name: "task_complete", Pattern: `(?i)(task|plan)\s+complete`, Event: "Task completed", IdleSeconds: 0, CooldownSeconds: 60, Severity: "info"},
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
	case "webhook":
		return sendWebhook(cfg.Providers.Webhook, n)
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
	case "webhook":
		p := cfg.Providers.Webhook
		if !p.Enabled || strings.TrimSpace(p.URL) == "" {
			return errors.New("webhook provider is not fully configured")
		}
		return nil
	default:
		return fmt.Errorf("unknown provider: %s", provider)
	}
}

// --- Telegram interactive (bidirectional) ---

type telegramSendResult struct {
	OK     bool `json:"ok"`
	Result struct {
		MessageID int `json:"message_id"`
	} `json:"result"`
}

type telegramUpdate struct {
	UpdateID      int                    `json:"update_id"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramCallbackQuery struct {
	ID      string `json:"id"`
	From    struct{ ID int `json:"id"` } `json:"from"`
	Message struct {
		Chat struct{ ID int64 `json:"id"` } `json:"chat"`
	} `json:"message"`
	Data string `json:"data"`
}

type telegramUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

func sendTelegramInteractive(p TelegramProvider, n notification) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", p.BotToken)
	text := formatMessage(n)

	keyboard := map[string]interface{}{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "Yes", "callback_data": "y"},
				{"text": "No", "callback_data": "n"},
			},
		},
	}
	kbJSON, _ := json.Marshal(keyboard)

	payload := url.Values{}
	payload.Set("chat_id", p.ChatID)
	payload.Set("text", text)
	payload.Set("disable_web_page_preview", "true")
	payload.Set("reply_markup", string(kbJSON))

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

func pollTelegramCallbacks(ctx context.Context, p TelegramProvider, replyCh chan<- string) {
	client := &http.Client{Timeout: 35 * time.Second}
	offset := 0
	chatID, _ := strconv.ParseInt(p.ChatID, 10, 64)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&allowed_updates=[\"callback_query\"]&offset=%d", p.BotToken, offset)
		resp, err := client.Get(endpoint)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if err != nil {
			continue
		}

		var updates telegramUpdatesResponse
		if err := json.Unmarshal(body, &updates); err != nil || !updates.OK {
			continue
		}

		for _, u := range updates.Result {
			offset = u.UpdateID + 1
			if u.CallbackQuery == nil {
				continue
			}
			cb := u.CallbackQuery
			if cb.Message.Chat.ID != chatID {
				continue
			}
			answerCallbackQuery(p.BotToken, cb.ID)
			select {
			case replyCh <- cb.Data:
			case <-ctx.Done():
				return
			}
		}
	}
}

func answerCallbackQuery(token, callbackID string) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	payload := url.Values{}
	payload.Set("callback_query_id", callbackID)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.PostForm(endpoint, payload)
	if err != nil {
		return
	}
	resp.Body.Close()
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

func sendWebhook(p WebhookProvider, n notification) error {
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = "POST"
	}
	timeout := p.TimeoutMilli
	if timeout <= 0 {
		timeout = 8000
	}

	payload := map[string]string{
		"title":     n.Title,
		"body":      n.Body,
		"severity":  strings.ToLower(strings.TrimSpace(n.Severity)),
		"source":    "knock",
		"timestamp": time.Now().Format(time.RFC3339),
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, p.URL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.AuthHeader != "" && p.AuthValue != "" {
		req.Header.Set(p.AuthHeader, p.AuthValue)
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("webhook status=%d body=%q", resp.StatusCode, string(body))
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
	if envPath := os.Getenv("KNOCK_CONFIG_PATH"); envPath != "" {
		return envPath, nil
	}
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

// --- update check ---

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func cmdUpdate(args []string) error {
	if len(args) < 1 || args[0] != "check" {
		return errors.New("usage: knock update check [--quiet]")
	}

	fs := flag.NewFlagSet("update check", flag.ContinueOnError)
	quiet := fs.Bool("quiet", false, "only print when update available")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	latest, err := fetchLatestVersion()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	// persist check timestamp
	cfg, _ := loadOrDefaultConfig()
	cfg.Metadata.Update.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	cfg.Metadata.Update.LatestVersion = latest
	_, _ = saveConfig(cfg)

	if isNewerVersion(latest, appVersion) {
		fmt.Printf("Update available: %s -> %s\n", appVersion, latest)
		fmt.Printf("  https://github.com/zacfire/knock/releases/latest\n")
		return nil
	}

	if !*quiet {
		fmt.Printf("knock %s is up to date\n", appVersion)
	}
	return nil
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(updateRepoURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github api status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// isNewerVersion returns true if latest > current using simple numeric comparison.
func isNewerVersion(latest, current string) bool {
	latestParts := strings.Split(latest, ".")
	currentParts := strings.Split(current, ".")
	maxLen := len(latestParts)
	if len(currentParts) > maxLen {
		maxLen = len(currentParts)
	}
	for i := 0; i < maxLen; i++ {
		var l, c int
		if i < len(latestParts) {
			l, _ = strconv.Atoi(latestParts[i])
		}
		if i < len(currentParts) {
			c, _ = strconv.Atoi(currentParts[i])
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

func maybeRunPassiveUpdateReminder(command string) {
	if command == "update" {
		return
	}

	cfg, err := loadOrDefaultConfig()
	if err != nil {
		return
	}

	// if we already know about a newer version, remind (at most once per 24h)
	if cfg.Metadata.Update.LatestVersion != "" && isNewerVersion(cfg.Metadata.Update.LatestVersion, appVersion) {
		lastNoticed, _ := time.Parse(time.RFC3339, cfg.Metadata.Update.LastNoticedAt)
		if time.Since(lastNoticed) > updateCheckPeriod {
			fmt.Fprintf(os.Stderr, "hint: knock %s available (current: %s). Run: knock update check\n", cfg.Metadata.Update.LatestVersion, appVersion)
			cfg.Metadata.Update.LastNoticedAt = time.Now().UTC().Format(time.RFC3339)
			_, _ = saveConfig(cfg)
		}
	}

	// trigger background check if stale
	lastChecked, _ := time.Parse(time.RFC3339, cfg.Metadata.Update.LastCheckedAt)
	if time.Since(lastChecked) > updateCheckPeriod {
		go func() {
			latest, err := fetchLatestVersion()
			if err != nil {
				return
			}
			c, err := loadOrDefaultConfig()
			if err != nil {
				return
			}
			c.Metadata.Update.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
			c.Metadata.Update.LatestVersion = latest
			_, _ = saveConfig(c)
		}()
	}
}
