package publicapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func makeStdToken(t *testing.T, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal token payload: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func makeURLToken(t *testing.T, payload any, padded bool) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal token payload: %v", err)
	}
	if padded {
		return base64.URLEncoding.EncodeToString(raw)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestValidateTunnelTokenValid(t *testing.T) {
	stdToken := makeStdToken(t, map[string]any{"a": "account", "t": "tunnel", "s": "secret"})
	// Payload chosen so standard base64 contains '+' and '/' while base64url contains '-' and '_'.
	specialPayload := map[string]any{"a": "acc???~~~", "t": "tun~~~???", "s": "sec???~~~"}
	stdSpecial := makeStdToken(t, specialPayload)
	if !strings.Contains(stdSpecial, "+") || !strings.Contains(stdSpecial, "/") {
		t.Fatalf("expected standard base64 token to contain '+' and '/', got %q", stdSpecial)
	}
	urlSpecialPadded := makeURLToken(t, specialPayload, true)
	urlSpecialUnpadded := makeURLToken(t, specialPayload, false)
	if !strings.Contains(urlSpecialUnpadded, "-") || !strings.Contains(urlSpecialUnpadded, "_") {
		t.Fatalf("expected urlsafe base64 token to contain '-' and '_', got %q", urlSpecialUnpadded)
	}

	// Cover all three valid unpadded remainder classes: len%4 == 0, 2, 3.
	rem2 := makeURLToken(t, map[string]any{"a": "1", "t": "2", "s": "3"}, false)
	rem3 := makeURLToken(t, map[string]any{"a": "12", "t": "2", "s": "3"}, false)
	rem0 := makeURLToken(t, map[string]any{"a": "123", "t": "2", "s": "3"}, false)
	if len(rem2)%4 != 2 || len(rem3)%4 != 3 || len(rem0)%4 != 0 {
		t.Fatalf("unexpected remainder classes: %d, %d, %d", len(rem2)%4, len(rem3)%4, len(rem0)%4)
	}

	extraFieldsToken := makeURLToken(t, map[string]any{
		"a": "acct-id",
		"t": "tunnel-id",
		"s": "密钥-secret",
		"e": "extra",
		"n": 42,
	}, false)

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "raw standard base64 token",
			input: stdToken,
			want:  stdToken,
		},
		{
			name:  "raw standard base64 with plus and slash",
			input: stdSpecial,
			want:  stdSpecial,
		},
		{
			name:  "raw base64url padded with minus and underscore",
			input: urlSpecialPadded,
			want:  urlSpecialPadded,
		},
		{
			name:  "raw base64url unpadded",
			input: urlSpecialUnpadded,
			want:  urlSpecialUnpadded,
		},
		{
			name:  "unpadded remainder 2",
			input: rem2,
			want:  rem2,
		},
		{
			name:  "unpadded remainder 3",
			input: rem3,
			want:  rem3,
		},
		{
			name:  "unpadded remainder 0",
			input: rem0,
			want:  rem0,
		},
		{
			name:  "linux install command",
			input: "sudo cloudflared service install " + stdToken,
			want:  stdToken,
		},
		{
			name:  "windows quoted install command with surrounding whitespace",
			input: "  \r\ncloudflared.exe service install \"" + urlSpecialUnpadded + "\"\r\n  ",
			want:  urlSpecialUnpadded,
		},
		{
			name:  "token flag with equals prefix",
			input: "cloudflared tunnel run --token=" + stdToken,
			want:  stdToken,
		},
		{
			name:  "multiline dashboard snippet",
			input: "# Install cloudflared\nbrew install cloudflared &&\nsudo cloudflared service install " + stdToken + "\n",
			want:  stdToken,
		},
		{
			name:  "duplicate identical token deduplicated",
			input: "sudo cloudflared service install " + stdToken + "\n# backup: " + stdToken,
			want:  stdToken,
		},
		{
			name:  "extra json keys and utf8 value",
			input: extraFieldsToken,
			want:  extraFieldsToken,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTunnelToken(tc.input)
			if err != nil {
				t.Fatalf("ValidateTunnelToken(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateTunnelToken(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateTunnelTokenAmbiguous(t *testing.T) {
	token1 := makeStdToken(t, map[string]any{"a": "account", "t": "tunnel", "s": "secret"})
	token2 := makeStdToken(t, map[string]any{"a": "account2", "t": "tunnel2", "s": "secret2"})
	mutated := token1[:len(token1)-3] + "ZZZ"

	const wantMsg = "请粘贴 Cloudflare 的完整安装命令或唯一的 Tunnel Token"
	inputs := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t  "},
		{"no token", "exampletoken"},
		{"install command missing token", "sudo cloudflared service install"},
		{"too short after eyJ", "eyJ" + strings.Repeat("A", 19)},
		{"two distinct valid tokens", token1 + " " + token2},
		{"token plus mutated copy", token1 + " " + mutated},
		{"valid token plus second eyJ blob", token1 + " eyJ" + strings.Repeat("B", 24)},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTunnelToken(tc.input)
			if err == nil {
				t.Fatalf("ValidateTunnelToken(%q) = %q, want error", tc.input, got)
			}
			if !errors.Is(err, ErrTunnelTokenAmbiguous) {
				t.Fatalf("error = %v, want errors.Is(_, ErrTunnelTokenAmbiguous)", err)
			}
			if err.Error() != wantMsg {
				t.Fatalf("error message = %q, want %q", err.Error(), wantMsg)
			}
		})
	}
}

func TestValidateTunnelTokenInvalid(t *testing.T) {
	const wantMsg = "未识别到有效的 Cloudflare Tunnel Token"

	invalidUTF8JSON := append([]byte(`{"a":"`), 0xff, 0xfe)
	invalidUTF8JSON = append(invalidUTF8JSON, []byte(`","t":"2","s":"3"}`)...)

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "corrupt base64 length mod 4 equals 1",
			input: "eyJ" + strings.Repeat("A", 22),
		},
		{
			name:  "invalid mid-token padding",
			input: "eyJhIjoiMSIsInQiOiIy=IiwicyI6IjMifQ==",
		},
		{
			name:  "excessive trailing padding",
			input: base64.StdEncoding.EncodeToString([]byte(`{"a":"1","t":"2","s":"3"}`)) + "====",
		},
		{
			name:  "invalid json syntax",
			input: base64.StdEncoding.EncodeToString([]byte(`{"a":"account","t":"tunnel",`)),
		},
		{
			name:  "trailing data after json object",
			input: base64.StdEncoding.EncodeToString([]byte(`{"a":"1","t":"2","s":"3"}extra`)),
		},
		{
			name:  "invalid utf8 bytes in json",
			input: base64.StdEncoding.EncodeToString(invalidUTF8JSON),
		},
		{
			name:  "missing key a",
			input: makeStdToken(t, map[string]any{"t": "tunnel", "s": "secret"}),
		},
		{
			name:  "missing key t",
			input: makeStdToken(t, map[string]any{"a": "account", "s": "secret"}),
		},
		{
			name:  "missing key s",
			input: makeStdToken(t, map[string]any{"a": "account", "t": "tunnel"}),
		},
		{
			name:  "empty string value",
			input: makeStdToken(t, map[string]any{"a": "", "t": "tunnel", "s": "secret"}),
		},
		{
			name:  "numeric value",
			input: makeStdToken(t, map[string]any{"a": 123, "t": "tunnel", "s": "secret"}),
		},
		{
			name:  "boolean value",
			input: makeStdToken(t, map[string]any{"a": "account", "t": true, "s": "secret"}),
		},
		{
			name:  "null value",
			input: makeStdToken(t, map[string]any{"a": "account", "t": "tunnel", "s": nil}),
		},
		{
			name:  "array value",
			input: makeStdToken(t, map[string]any{"a": []string{"x"}, "t": "tunnel", "s": "secret"}),
		},
		{
			name:  "object value",
			input: makeStdToken(t, map[string]any{"a": map[string]string{"k": "v"}, "t": "tunnel", "s": "secret"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTunnelToken(tc.input)
			if err == nil {
				t.Fatalf("ValidateTunnelToken(%q) = %q, want error", tc.input, got)
			}
			if !errors.Is(err, ErrTunnelTokenInvalid) {
				t.Fatalf("error = %v, want errors.Is(_, ErrTunnelTokenInvalid)", err)
			}
			if err.Error() != wantMsg {
				t.Fatalf("error message = %q, want %q", err.Error(), wantMsg)
			}
		})
	}
}

func TestValidatePublicOriginValid(t *testing.T) {
	label63 := "a" + strings.Repeat("b", 61) + "c"
	tld63 := strings.Repeat("z", 63)

	cases := []struct {
		input string
		want  string
	}{
		{"api.example.com", "https://api.example.com"},
		{"api.example.com/", "https://api.example.com"},
		{"api.example.com/v1", "https://api.example.com"},
		{"api.example.com/v1/", "https://api.example.com"},
		{"api.example.com/v1///", "https://api.example.com"},
		{"https://api.example.com", "https://api.example.com"},
		{"https://api.example.com/", "https://api.example.com"},
		{"https://api.example.com/v1", "https://api.example.com"},
		{"https://api.example.com/v1/", "https://api.example.com"},
		{"  API.Example.COM/  ", "https://api.example.com"},
		{"\thttps://API.Example.COM/v1/\r\n", "https://api.example.com"},
		{"sub.api.example.co.uk", "https://sub.api.example.co.uk"},
		{"a.b.cd", "https://a.b.cd"},
		{"api-1.v2-prod.example.com", "https://api-1.v2-prod.example.com"},
		{"xn--bcher-kva.example", "https://xn--bcher-kva.example"},
		{label63 + "." + tld63, "https://" + label63 + "." + tld63},
		{"nottrycloudflare.com", "https://nottrycloudflare.com"},
		{"my-trycloudflare.com", "https://my-trycloudflare.com"},
		{"my.notlocalhost.com", "https://my.notlocalhost.com"},
		{"local.example.com", "https://local.example.com"},
		{"test.example.com", "https://test.example.com"},
		{"internal.example.com", "https://internal.example.com"},
		{"invalid.example.com", "https://invalid.example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ValidatePublicOrigin(tc.input)
			if err != nil {
				t.Fatalf("ValidatePublicOrigin(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ValidatePublicOrigin(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidatePublicOriginInvalid(t *testing.T) {
	const wantMsg = "请填写公网 HTTPS 固定域名，例如 api.example.com（不含端口或其他路径，不支持 trycloudflare.com 临时域名）"
	label64 := strings.Repeat("a", 64)
	tld64 := strings.Repeat("z", 64)

	inputs := []string{
		"",
		"   ",
		"///",
		"https://",
		"https:///",
		"//api.example.com",
		"http://example.com",
		"HTTP://example.com",
		"HTTPS://api.example.com",
		"ftp://api.example.com",
		"127.0.0.1",
		"https://1.2.3.4",
		"https://[::1]",
		"https://user@evil.com",
		"https://user:pass@api.example.com",
		"https://user:@api.example.com",
		"https://:pass@api.example.com",
		"https://@api.example.com",
		"localhost",
		"api.localhost",
		"api.LOCALHOST",
		"api.local",
		"api.LOCAL",
		"api.internal",
		"api.test",
		"api.invalid",
		"api.example.com:7860",
		"https://api.example.com:443",
		"https://api.example.com:0",
		"https://api.example.com:",
		"api.example.com/path",
		"api.example.com/v1/chat/completions",
		"api.example.com/V1",
		"https://api.example.com//v1",
		"api.example.com?token=secret",
		"https://api.example.com?",
		"https://api.example.com/?",
		"https://api.example.com/v1?x=1",
		"https://api.example.com#frag",
		"https://api.example.com#",
		"https://api.example.com/#",
		"https://api%2eexample.com",
		"https://api.example.com/%761",
		"demo.trycloudflare.com",
		"sub.demo.trycloudflare.com",
		"https://DEMO.TRYCLOUDFLARE.COM",
		"trycloudflare.com",
		"https://TRYCLOUDFLARE.COM",
		"https://-a.example.com",
		"https://a-.example.com",
		"https://api..example.com",
		"https://.example.com",
		"https://api.example.com.",
		"https://api_1.example.com",
		"https://api.example.c",
		"https://api.example.123",
		"https://" + label64 + ".example.com",
		"https://api." + tld64,
		"https://中文.example.com",
		"https://api.exam ple.com",
		"https://api.example.com /",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := ValidatePublicOrigin(input)
			if err == nil {
				t.Fatalf("ValidatePublicOrigin(%q) = %q, want error", input, got)
			}
			if !errors.Is(err, ErrPublicOriginInvalid) {
				t.Fatalf("ValidatePublicOrigin(%q) error = %v, want ErrPublicOriginInvalid", input, err)
			}
			if err.Error() != wantMsg {
				t.Fatalf("ValidatePublicOrigin(%q) error msg = %q, want %q", input, err.Error(), wantMsg)
			}
		})
	}
}

func TestColabOnePublicConfigValidateAndJSON(t *testing.T) {
	token := makeStdToken(t, map[string]any{"a": "account", "t": "tunnel", "s": "secret"})
	key := "co_" + strings.Repeat("k", 48)

	cfg := ColabOnePublicConfig{
		Mode:        ModeNamed,
		Domain:      "api.example.com/v1/",
		TunnelToken: "sudo cloudflared service install " + token,
		APIKey:      key,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
	if cfg.Mode != "named" {
		t.Fatalf("Mode = %q, want named", cfg.Mode)
	}
	if cfg.Domain != "https://api.example.com" {
		t.Fatalf("Domain = %q, want https://api.example.com", cfg.Domain)
	}
	if cfg.TunnelToken != token {
		t.Fatalf("TunnelToken = %q, want %q", cfg.TunnelToken, token)
	}

	// Verify exact JSON field names match Python Pydantic model_dump().
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal map: %v", err)
	}
	wantFields := map[string]string{
		"mode":         "named",
		"domain":       "https://api.example.com",
		"tunnel_token": token,
		"api_key":      key,
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("JSON keys = %v, want %v", fields, wantFields)
	}
	for k, wantVal := range wantFields {
		if fields[k] != wantVal {
			t.Fatalf("JSON[%q] = %q, want %q", k, fields[k], wantVal)
		}
	}

	// Verify String/GoString never leak secrets.
	for _, formatted := range []string{
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%s", cfg),
		fmt.Sprintf("%v", &cfg),
	} {
		if strings.Contains(formatted, token) || strings.Contains(formatted, key) {
			t.Fatalf("formatted config leaked secret: %s", formatted)
		}
	}
	var nilCfg *ColabOnePublicConfig
	if got := fmt.Sprintf("%v", nilCfg); got != "<nil>" {
		t.Fatalf("nil config formatted as %q, want <nil>", got)
	}
	if err := nilCfg.Validate(); !errors.Is(err, ErrPublicModeInvalid) {
		t.Fatalf("nil Validate() = %v, want ErrPublicModeInvalid", err)
	}
}

func TestColabOnePublicConfigValidationErrorsAndAtomicity(t *testing.T) {
	token := makeStdToken(t, map[string]any{"a": "account", "t": "tunnel", "s": "secret"})
	keyMin := "co_" + strings.Repeat("aB0_-", 8)  // 40 chars
	keyMax := "co_" + strings.Repeat("aB0_-", 20) // 100 chars

	for _, key := range []string{keyMin, keyMax} {
		cfg := ColabOnePublicConfig{
			Mode:        ModeNamed,
			Domain:      "api.example.com",
			TunnelToken: token,
			APIKey:      key,
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("boundary API key len=%d failed: %v", len(key), err)
		}
	}

	cases := []struct {
		name    string
		cfg     ColabOnePublicConfig
		wantErr error
	}{
		{
			name: "quick mode rejected",
			cfg: ColabOnePublicConfig{
				Mode:        "quick",
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      keyMin,
			},
			wantErr: ErrPublicModeInvalid,
		},
		{
			name: "empty mode rejected",
			cfg: ColabOnePublicConfig{
				Mode:        "",
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      keyMin,
			},
			wantErr: ErrPublicModeInvalid,
		},
		{
			name: "missing api key",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: token,
			},
			wantErr: ErrAPIKeyInvalid,
		},
		{
			name: "api key missing prefix",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      "sk_" + strings.Repeat("k", 48),
			},
			wantErr: ErrAPIKeyInvalid,
		},
		{
			name: "api key too short 39 chars",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      "co_" + strings.Repeat("k", 39),
			},
			wantErr: ErrAPIKeyInvalid,
		},
		{
			name: "api key too long 101 chars",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      "co_" + strings.Repeat("k", 101),
			},
			wantErr: ErrAPIKeyInvalid,
		},
		{
			name: "api key invalid char",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      "co_" + strings.Repeat("k", 47) + "+",
			},
			wantErr: ErrAPIKeyInvalid,
		},
		{
			name: "api key trailing newline",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: token,
				APIKey:      "co_" + strings.Repeat("k", 48) + "\n",
			},
			wantErr: ErrAPIKeyInvalid,
		},
		{
			name: "missing domain",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				TunnelToken: token,
				APIKey:      keyMin,
			},
			wantErr: ErrPublicOriginInvalid,
		},
		{
			name: "domain exceeds max length",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      strings.Repeat("a.", 151) + "com",
				TunnelToken: token,
				APIKey:      keyMin,
			},
			wantErr: ErrPublicOriginInvalid,
		},
		{
			name: "missing tunnel token",
			cfg: ColabOnePublicConfig{
				Mode:   ModeNamed,
				Domain: "api.example.com",
				APIKey: keyMin,
			},
			wantErr: ErrTunnelTokenAmbiguous,
		},
		{
			name: "tunnel token exceeds max length",
			cfg: ColabOnePublicConfig{
				Mode:        ModeNamed,
				Domain:      "api.example.com",
				TunnelToken: strings.Repeat(" ", MaxTunnelTokenLength+1) + token,
				APIKey:      keyMin,
			},
			wantErr: ErrTunnelTokenInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := cfg.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// Atomicity check: invalid token must not leave Domain mutated.
	origDomain := "api.example.com/v1/"
	origToken := "invalid-token"
	cfg := ColabOnePublicConfig{
		Mode:        ModeNamed,
		Domain:      origDomain,
		TunnelToken: origToken,
		APIKey:      keyMin,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for invalid token")
	}
	if cfg.Domain != origDomain || cfg.TunnelToken != origToken {
		t.Fatalf("Validate() partially mutated config on error: domain=%q token=%q", cfg.Domain, cfg.TunnelToken)
	}
}

func TestConcurrentValidation(t *testing.T) {
	token := makeURLToken(t, map[string]any{"a": "acc", "t": "tun", "s": "sec"}, false)
	key := "co_" + strings.Repeat("x", 48)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cfg := ColabOnePublicConfig{
					Mode:        ModeNamed,
					Domain:      "API.Example.COM/v1/",
					TunnelToken: "sudo cloudflared service install " + token,
					APIKey:      key,
				}
				if err := cfg.Validate(); err != nil {
					t.Errorf("concurrent Validate() failed: %v", err)
					return
				}
				if cfg.Domain != "https://api.example.com" || cfg.TunnelToken != token {
					t.Errorf("unexpected normalized config: %+v", cfg)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestColabOnePublicConfigStrictUnmarshalJSON(t *testing.T) {
	token := makeStdToken(t, map[string]any{"a": "acc", "t": "tun", "s": "sec"})
	key := "co_" + strings.Repeat("k", 48)

	// Omitted "mode" defaults to "named".
	var defaulted ColabOnePublicConfig
	rawOmitted := `{"domain":"api.example.com","tunnel_token":"` + token + `","api_key":"` + key + `"}`
	if err := json.Unmarshal([]byte(rawOmitted), &defaulted); err != nil {
		t.Fatalf("unmarshal omitted mode failed: %v", err)
	}
	if defaulted.Mode != ModeNamed || defaulted.Validate() != nil {
		t.Fatalf("omitted mode did not default/validate: %+v", defaulted)
	}

	// Explicit "mode": "", "mode": null, top-level null, and unknown fields are all rejected.
	for _, bad := range []string{
		`null`,
		`{"mode":"","domain":"api.example.com","tunnel_token":"` + token + `","api_key":"` + key + `"}`,
		`{"mode":null,"domain":"api.example.com","tunnel_token":"` + token + `","api_key":"` + key + `"}`,
		`{"domain":"api.example.com","tunnel_token":"` + token + `","api_key":"` + key + `","extra":1}`,
	} {
		var cfg ColabOnePublicConfig
		if err := json.Unmarshal([]byte(bad), &cfg); err == nil {
			t.Fatalf("expected strict UnmarshalJSON failure for %s", bad)
		}
	}
}
