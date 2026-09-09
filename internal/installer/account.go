package installer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// AccountStatus is the read-only, live subscription snapshot used by install UI.
type AccountStatus struct {
	Models            []ModelOption
	DefaultModel      string
	Hooks             []HookStatus
	HookWarnings      []string
	HooksEnabled      bool
	HooksFeatureKnown bool
}

type HookStatus struct {
	EventName     string
	StatusMessage string
	SourcePath    string
	Enabled       bool
	TrustStatus   string
}

type accountModelPage struct {
	Data []struct {
		Model     string `json:"model"`
		Default   string `json:"defaultReasoningEffort"`
		IsDefault bool   `json:"isDefault"`
		Efforts   []struct {
			Name string `json:"reasoningEffort"`
		} `json:"supportedReasoningEfforts"`
	} `json:"data"`
	Next *string `json:"nextCursor"`
}

func decodeAccountModelPage(raw []byte) (accountModelPage, error) {
	var p accountModelPage
	if json.Unmarshal(raw, &p) != nil {
		return p, errors.New(emptyModelCatalog)
	}
	for _, m := range p.Data {
		if strings.TrimSpace(m.Model) == "" || strings.TrimSpace(m.Default) == "" || len(m.Efforts) == 0 {
			return p, errors.New("app-server returned malformed model catalog")
		}
	}
	return p, nil
}

const accountProbeLimit = 8 * 1024 * 1024
const emptyModelCatalog = "app-server returned empty model catalog"

const (
	errTransport   = "app-server transport failed"
	errUnavailable = "app-server unavailable"
	codexHomeEnv   = "CODEX_HOME="
)

var accountProbe = probeAccount

func probeAccount(home string) (*AccountStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return probeAccountContext(ctx, home)
}

type rpcClient struct {
	stdin io.Writer
	scan  *bufio.Scanner
	next  int
}

func initializeAccount(c *rpcClient) error {
	if _, err := c.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "codex-setup", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}); err != nil {
		return err
	}
	if _, err := io.WriteString(c.stdin, "{\"method\":\"initialized\"}\n"); err != nil {
		return errors.New(errTransport)
	}
	raw, err := c.call("account/read", map[string]any{"refreshToken": false})
	if err != nil {
		return err
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if json.Unmarshal(raw, &account) != nil || account.Account == nil || account.Account.Type != "chatgpt" {
		return errors.New("ChatGPT login required")
	}
	return nil
}

func (c *rpcClient) call(method string, params any) (json.RawMessage, error) {
	c.next++
	id := c.next
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err = fmt.Fprintf(c.stdin, "%s\n", b); err != nil {
		return nil, errors.New(errTransport)
	}
	for c.scan.Scan() {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(c.scan.Bytes(), &msg) != nil {
			return nil, errors.New("app-server returned malformed data")
		}
		if msg.Method != "" || msg.ID != id {
			continue
		}
		if len(msg.Error) != 0 && string(msg.Error) != "null" {
			return nil, errors.New("app-server request failed")
		}
		if len(msg.Result) == 0 || string(msg.Result) == "null" {
			return nil, errors.New("app-server returned empty data")
		}
		return msg.Result, nil
	}
	if err := c.scan.Err(); err != nil {
		return nil, errors.New(errTransport)
	}
	return nil, errors.New("app-server closed connection")
}

func probeAccountContext(ctx context.Context, home string) (*AccountStatus, error) {
	if home == "" {
		return nil, errors.New("CODEX_HOME is required")
	}
	if err := validateAccountConfig(home); err != nil {
		return nil, err
	}
	env := os.Environ()
	for i, item := range env {
		if strings.HasPrefix(item, codexHomeEnv) {
			env[i] = codexHomeEnv + home
			break
		}
	}
	if !containsEnv(env, codexHomeEnv) {
		env = append(env, codexHomeEnv+home)
	}
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New(errUnavailable)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.New(errUnavailable)
	}
	if err = cmd.Start(); err != nil {
		return nil, errors.New(errUnavailable)
	}
	stopPipes := context.AfterFunc(ctx, func() { _ = in.Close(); _ = out.Close(); _ = cmd.Process.Kill() })
	defer stopPipes()
	defer func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	s := bufio.NewScanner(io.LimitReader(out, accountProbeLimit))
	s.Buffer(make([]byte, 4096), 1024*1024)
	c := &rpcClient{stdin: in, scan: s}
	if err := initializeAccount(c); err != nil {
		return nil, err
	}
	status := &AccountStatus{}
	if err := readAccountModels(c, status); err != nil {
		return nil, err
	}
	if len(status.Models) == 0 {
		return nil, errors.New(emptyModelCatalog)
	}
	if status.DefaultModel == "" {
		status.DefaultModel = status.Models[0].Model
	}
	readAccountHooks(c, status)
	return status, nil
}

func readAccountHooks(c *rpcClient, status *AccountStatus) {
	if raw, e := c.call("hooks/list", map[string]any{}); e == nil {
		parseHooks(status, raw)
	} else {
		status.HookWarnings = append(status.HookWarnings, "hooks unavailable")
	}
	if raw, e := c.call("config/read", map[string]any{"includeLayers": false}); e == nil {
		var cfg struct {
			Config struct {
				Features struct {
					Hooks *bool `json:"hooks"`
				} `json:"features"`
			} `json:"config"`
		}
		if json.Unmarshal(raw, &cfg) == nil && cfg.Config.Features.Hooks != nil {
			status.HooksFeatureKnown, status.HooksEnabled = true, *cfg.Config.Features.Hooks
		} else {
			status.HookWarnings = append(status.HookWarnings, "hook feature status unknown")
		}
	} else {
		status.HookWarnings = append(status.HookWarnings, "hook feature status unknown")
	}
}

func validateAccountConfig(home string) error {
	b, err := os.ReadFile(home + string(os.PathSeparator) + "config.toml")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot read config")
	}
	var config map[string]any
	if err := toml.Unmarshal(b, &config); err != nil {
		return errors.New("malformed config")
	}
	if v, ok := config["model_provider"].(string); ok && v != "" && v != "openai" {
		return errors.New("custom model provider/catalog must be removed before account verification")
	}
	for _, key := range []string{"model_providers", "model_catalog_json"} {
		if _, ok := config[key]; ok {
			return errors.New("custom model provider/catalog must be removed before account verification")
		}
	}
	return nil
}

func readAccountModels(c *rpcClient, status *AccountStatus) error {
	var cursor *string
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		params := map[string]any{"cursor": cursor}
		raw, e := c.call("model/list", params)
		if e != nil {
			return e
		}
		pageData, err := decodeAccountModelPage(raw)
		if err != nil {
			return err
		}
		if page == 0 && len(pageData.Data) == 0 {
			return errors.New(emptyModelCatalog)
		}
		if err := appendAccountModels(status, pageData); err != nil {
			return err
		}
		if pageData.Next == nil || *pageData.Next == "" {
			return nil
		}
		cursor = pageData.Next
		if seen[*cursor] {
			return errors.New("app-server repeated model cursor")
		}
		seen[*cursor] = true
	}
	return errors.New("app-server model catalog pagination limit exceeded")
}

func appendAccountModels(status *AccountStatus, page accountModelPage) error {
	for _, m := range page.Data {
		option := ModelOption{Model: m.Model}
		for _, effort := range m.Efforts {
			if effort.Name != "" {
				option.Efforts = append(option.Efforts, effort.Name)
			}
		}
		if len(option.Efforts) == 0 {
			return errors.New("app-server returned malformed model catalog")
		}
		status.Models = append(status.Models, option)
		if m.IsDefault && status.DefaultModel == "" {
			status.DefaultModel = m.Model
		}
	}
	return nil
}

func containsEnv(env []string, prefix string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func parseHooks(status *AccountStatus, raw []byte) {
	var result struct {
		Data []struct {
			Hooks []struct {
				EventName string  `json:"eventName"`
				Enabled   bool    `json:"enabled"`
				Trust     string  `json:"trustStatus"`
				Message   *string `json:"statusMessage"`
				Path      string  `json:"sourcePath"`
			} `json:"hooks"`
			Errors   []struct{ Message, Path string } `json:"errors"`
			Warnings []string                         `json:"warnings"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil {
		status.HookWarnings = append(status.HookWarnings, "hooks returned malformed data")
		return
	}
	if result.Data == nil {
		status.HookWarnings = append(status.HookWarnings, "hooks returned malformed data")
		return
	}
	for _, entry := range result.Data {
		for _, h := range entry.Hooks {
			msg := ""
			if h.Message != nil {
				msg = *h.Message
			}
			status.Hooks = append(status.Hooks, HookStatus{EventName: h.EventName, StatusMessage: msg, SourcePath: h.Path, Enabled: h.Enabled, TrustStatus: h.Trust})
		}
		for range entry.Errors {
			status.HookWarnings = append(status.HookWarnings, "hook error reported")
		}
		for range entry.Warnings {
			status.HookWarnings = append(status.HookWarnings, "hook warning reported")
		}
	}
}

func ReadAccount(codexHome string) (*AccountStatus, error) { return accountProbe(codexHome) }

func ResolveAccountChoices(status *AccountStatus, requested map[string]ModelChoice) (map[string]ModelChoice, []string, error) {
	roles := make(map[string]bool)
	for _, role := range ModelRoles {
		roles[role.ID] = true
	}
	return resolveAccountChoices(status, requested, roles)
}

func resolveAccountChoices(status *AccountStatus, requested map[string]ModelChoice, roles map[string]bool) (map[string]ModelChoice, []string, error) {
	if status == nil || len(status.Models) == 0 {
		return nil, nil, errors.New("account catalog is required")
	}
	known := map[string]bool{}
	for _, role := range ModelRoles {
		known[role.ID] = true
	}
	for id := range requested {
		if !known[id] {
			return nil, nil, fmt.Errorf("rol desconocido: %s", id)
		}
	}
	available := func(c ModelChoice) bool { return modelChoiceAvailable(status, c) }
	defaults := DefaultModelChoices()
	choices, warnings := map[string]ModelChoice{}, []string{}
	for _, role := range ModelRoles {
		if !roles[role.ID] {
			continue
		}
		c, warning, err := chooseAccountRole(role.ID, requested, defaults, available, status)
		if err != nil {
			return nil, nil, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		choices[role.ID] = c
	}
	return choices, warnings, nil
}

func modelChoiceAvailable(status *AccountStatus, c ModelChoice) bool {
	for _, m := range status.Models {
		if m.Model == c.Model {
			return hasString(m.Efforts, c.Effort)
		}
	}
	return false
}

func chooseAccountRole(id string, requested map[string]ModelChoice, defaults map[string]ModelChoice, available func(ModelChoice) bool, status *AccountStatus) (ModelChoice, string, error) {
	if c, ok := requested[id]; ok {
		if !available(c) {
			return ModelChoice{}, "", fmt.Errorf("modelo no disponible para %s: %s/%s; elige una opción del catálogo", id, c.Model, c.Effort)
		}
		return c, "", nil
	}
	c := defaults[id]
	if available(c) {
		return c, "", nil
	}
	if c.Model == "gpt-5.3-codex-spark" {
		luna := ModelChoice{"gpt-5.6-luna", "medium"}
		if available(luna) {
			return luna, fmt.Sprintf("%s sustituido por gpt-5.6-luna/medium", id), nil
		}
	}
	if id == "fallback_explorer" || id == "fallback_executor" {
		return ModelChoice{}, "", fmt.Errorf("luna no está disponible para %s; elige explícitamente un modelo de respaldo", id)
	}
	c = ModelChoice{status.DefaultModel, effortFor(status, status.DefaultModel)}
	if !available(c) {
		return ModelChoice{}, "", fmt.Errorf("el catálogo no ofrece un modelo válido para %s", id)
	}
	return c, fmt.Sprintf("%s sustituido por %s/%s", id, c.Model, c.Effort), nil
}
func effortFor(status *AccountStatus, model string) string {
	for _, m := range status.Models {
		if m.Model == model {
			if hasString(m.Efforts, "medium") {
				return "medium"
			}
			return m.Efforts[0]
		}
	}
	return ""
}

func (e *Engine) BuildPlanWithAccount(ids []string, requested map[string]ModelChoice, status *AccountStatus) (*Plan, error) {
	modules, err := e.Resolve(ids)
	if err != nil {
		return nil, err
	}
	roles := map[string]bool{"principal": true, "default": true}
	for _, module := range modules {
		if module.ID == "prewalk" {
			for _, role := range ModelRoles {
				roles[role.ID] = true
			}
		}
	}
	choices, warnings, err := resolveAccountChoices(status, requested, roles)
	if err != nil {
		return nil, err
	}
	p, err := e.buildPlanWithOptions(ids, choices, status.Models)
	if p != nil {
		p.Warnings = append(p.Warnings, warnings...)
	}
	return p, err
}
