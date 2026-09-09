package installer

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeAccountContextDeadlineClosesInheritedPipes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+old)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := probeAccountContext(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("deadline ignored: %v", time.Since(start))
	}
}

func TestRPCClientTranscriptToleratesNotificationsAndRejectsErrors(t *testing.T) {
	var sent bytes.Buffer
	c := &rpcClient{stdin: &sent, scan: bufio.NewScanner(bytes.NewBufferString("{\"method\":\"notice\"}\n{\"id\":1,\"result\":{\"ok\":true}}\n"))}
	raw, err := c.call("initialize", map[string]any{})
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
	if !bytes.Contains(sent.Bytes(), []byte(`"method":"initialize"`)) {
		t.Fatal("request not sent")
	}
	c = &rpcClient{stdin: &bytes.Buffer{}, scan: bufio.NewScanner(bytes.NewBufferString(`{"id":1,"error":{"message":"secret"}}` + "\n"))}
	if _, err = c.call("bad", nil); err == nil || bytes.Contains([]byte(err.Error()), []byte("secret")) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestInitializeAccountTranscriptValidatesLoginAndNotification(t *testing.T) {
	for _, tc := range []struct {
		name, account string
		wantErr       bool
	}{{"chatgpt", `{"type":"chatgpt"}`, false}, {"null", "null", true}, {"apikey", `{"type":"apiKey"}`, true}} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := `{"id":1,"result":{}}` + "\n" + `{"id":2,"result":{"account":` + tc.account + `}}` + "\n"
			var sent bytes.Buffer
			c := &rpcClient{stdin: &sent, scan: bufio.NewScanner(strings.NewReader(transcript))}
			err := initializeAccount(c)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if !bytes.Contains(sent.Bytes(), []byte(`{"method":"initialized"}`)) {
				t.Fatalf("initialized notification missing: %s", sent.Bytes())
			}
			if bytes.Contains(sent.Bytes(), []byte(`"id":0`)) {
				t.Fatal("notification had id")
			}
		})
	}
}

func TestRPCClientTranscriptRejectsMalformedAndEmpty(t *testing.T) {
	for _, input := range []string{"not-json\n", `{"id":1,"result":null}` + "\n"} {
		c := &rpcClient{stdin: &bytes.Buffer{}, scan: bufio.NewScanner(bytes.NewBufferString(input))}
		if _, err := c.call("x", nil); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}

func TestRPCClientTranscriptBoundsLines(t *testing.T) {
	c := &rpcClient{stdin: &bytes.Buffer{}, scan: bufio.NewScanner(strings.NewReader(strings.Repeat("x", 2*1024*1024) + "\n"))}
	if _, err := c.call("x", nil); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestReadAccountModelsPaginatesAndRejectsRepeatedCursor(t *testing.T) {
	page := `{"id":1,"result":{"data":[{"model":"one","defaultReasoningEffort":"medium","isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}],"nextCursor":"next"}}` + "\n" + `{"id":2,"result":{"data":[{"model":"two","defaultReasoningEffort":"low","isDefault":false,"supportedReasoningEfforts":[{"reasoningEffort":"low"}]}],"nextCursor":null}}` + "\n"
	c := &rpcClient{stdin: &bytes.Buffer{}, scan: bufio.NewScanner(strings.NewReader(page))}
	s := &AccountStatus{}
	if err := readAccountModels(c, s); err != nil || len(s.Models) != 2 {
		t.Fatalf("status=%#v err=%v", s, err)
	}
	repeated := `{"id":1,"result":{"data":[{"model":"one","defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}],"nextCursor":"same"}}` + "\n" + `{"id":2,"result":{"data":[],"nextCursor":"same"}}` + "\n"
	c = &rpcClient{stdin: &bytes.Buffer{}, scan: bufio.NewScanner(strings.NewReader(repeated))}
	if err := readAccountModels(c, &AccountStatus{}); err == nil {
		t.Fatal("repeated cursor accepted")
	}
}

func TestDecodeAccountModelPageRejectsInvalidModels(t *testing.T) {
	for _, raw := range []string{`{"data":[{"model":"x","defaultReasoningEffort":"medium","supportedReasoningEfforts":[]}]}`, `not-json`} {
		if _, err := decodeAccountModelPage([]byte(raw)); err == nil {
			t.Fatal("invalid model page accepted")
		}
	}
}

func TestResolveAccountChoicesHandlesSparkFallbackAndUnknownRoles(t *testing.T) {
	status := &AccountStatus{Models: []ModelOption{{modelLuna, []string{"medium"}}, {modelAstra, []string{"medium"}}}, DefaultModel: modelAstra}
	got, warnings, err := ResolveAccountChoices(status, map[string]ModelChoice{})
	if err != nil || got["fallback_explorer"].Model != modelLuna || len(warnings) == 0 {
		t.Fatalf("got=%#v warnings=%v err=%v", got, warnings, err)
	}
	if _, _, err = ResolveAccountChoices(status, map[string]ModelChoice{"nope": {modelAstra, "medium"}}); err == nil {
		t.Fatal("unknown role accepted")
	}
	status.Models = status.Models[1:]
	if _, _, err = ResolveAccountChoices(status, map[string]ModelChoice{}); err == nil {
		t.Fatal("missing Luna fallback accepted")
	}
}

func TestResolveAccountChoicesUsesLiveCatalogAndPreservesExplicit(t *testing.T) {
	const accountModel = "account-model"
	status := &AccountStatus{Models: []ModelOption{{accountModel, []string{"medium"}}, {modelLuna, []string{"medium"}}}, DefaultModel: accountModel}
	requested := map[string]ModelChoice{"principal": {accountModel, "medium"}}
	got, warnings, err := ResolveAccountChoices(status, requested)
	if err != nil {
		t.Fatal(err)
	}
	if got["principal"].Model != accountModel || len(warnings) == 0 {
		t.Fatalf("choices=%#v warnings=%#v", got, warnings)
	}
	if _, _, err = ResolveAccountChoices(status, map[string]ModelChoice{"principal": {"missing", "medium"}}); err == nil {
		t.Fatal("invalid explicit choice accepted")
	}
}

func TestParseHooksPreservesTrustAndWarnings(t *testing.T) {
	s := &AccountStatus{}
	parseHooks(s, []byte(`{"data":[{"hooks":[{"enabled":true,"trustStatus":"modified","statusMessage":"changed","sourcePath":"/tmp/hook"}],"errors":[{"message":"bad","path":"/tmp/x"}],"warnings":["warn"]}]}`))
	if len(s.Hooks) != 1 || !s.Hooks[0].Enabled || s.Hooks[0].TrustStatus != "modified" || len(s.HookWarnings) != 2 {
		t.Fatalf("%#v", s)
	}
}

func TestReadAccountHooksFeatureStates(t *testing.T) {
	for _, tc := range []struct {
		name, value    string
		known, enabled bool
	}{{"true", "true", true, true}, {"false", "false", true, false}, {"missing", "null", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := `{"id":1,"result":{"data":[]}}` + "\n" + `{"id":2,"result":{"config":{"features":{"hooks":` + tc.value + `}}}}` + "\n"
			c := &rpcClient{stdin: &bytes.Buffer{}, scan: bufio.NewScanner(strings.NewReader(transcript))}
			s := &AccountStatus{}
			readAccountHooks(c, s)
			if s.HooksFeatureKnown != tc.known || s.HooksEnabled != tc.enabled {
				t.Fatalf("status=%#v", s)
			}
		})
	}
}
