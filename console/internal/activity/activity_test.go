package activity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestActivityBoundedAndRedactsCredentials(t *testing.T) {
	log := NewColabOneActivityLog(2, "", 0)
	defer log.Close()

	log.Add("discarded", "", "")
	log.Add(
		"https://example.com/?code=sensitive Bearer bearer-secret hf_secret_value 4/secret-authorization-code-long",
		"",
		"",
	)
	log.Add("refresh_token=refresh-secret", "", "")

	items := log.Snapshot()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != 2 || items[1].ID != 3 {
		t.Fatalf("expected IDs [2 3], got [%d %d]", items[0].ID, items[1].ID)
	}
	if items[0].Source != "客户端" || items[0].Level != "info" {
		t.Fatalf("unexpected defaults: source=%q level=%q", items[0].Source, items[0].Level)
	}

	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	text := string(raw)
	for _, secret := range []string{
		"sensitive",
		"bearer-secret",
		"hf_secret_value",
		"secret-authorization",
		"refresh-secret",
	} {
		if strings.Contains(text, secret) {
			t.Errorf("secret %q leaked into snapshot: %s", secret, text)
		}
	}

	log.Clear()
	empty := log.Snapshot()
	if len(empty) != 0 {
		t.Fatalf("expected empty snapshot after Clear, got %d items", len(empty))
	}
	emptyJSON, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty snapshot: %v", err)
	}
	if string(emptyJSON) != "[]" {
		t.Fatalf("expected empty snapshot JSON [], got %s", string(emptyJSON))
	}

	// Sequence must continue monotonically even after Clear.
	log.Add("after clear", "GPU", "warn")
	after := log.Snapshot()
	if len(after) != 1 || after[0].ID != 4 {
		t.Fatalf("expected single entry with ID 4 after Clear, got %+v", after)
	}
}

func TestActivityPersistsRedactedLinesAndRotatesOnDisk(t *testing.T) {
	got := SafeMessage(
		"Failed to issue request POST https://colab.research.google.com/tun/m/assign?nbh=abc: Service Unavailable",
	)
	want := "Failed to issue request POST [链接已隐藏]: Service Unavailable"
	if got != want {
		t.Fatalf("SafeMessage URL with trailing colon:\n got: %q\nwant: %q", got, want)
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "activity.log")
	log := NewColabOneActivityLog(10, logFile, 16*1024)
	defer log.Close()

	log.Add(
		"Bearer super-secret-token https://example.com/?code=mycode hf_secret_abc",
		"GPU",
		"error",
	)

	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("expected log file permissions 0600, got %#o", perm)
		}
	}

	contentBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "[ERROR] [GPU]") {
		t.Errorf("missing expected [ERROR] [GPU] prefix in %q", content)
	}
	if !strings.Contains(content, "[链接已隐藏]") {
		t.Errorf("missing expected [链接已隐藏] marker in %q", content)
	}
	for _, leaked := range []string{"super-secret-token", "mycode", "hf_secret_abc", "https://example.com"} {
		if strings.Contains(content, leaked) {
			t.Errorf("secret %q leaked into disk log: %q", leaked, content)
		}
	}

	// Trigger rotation above maxBytes (16 KiB).
	payload := strings.Repeat("x", 180)
	for i := 0; i < 120; i++ {
		log.Add(fmt.Sprintf("entry-%03d %s", i, payload), "GPU", "info")
	}

	backup := logFile + ".1"
	backupInfo, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("expected rotated backup %s to exist: %v", backup, err)
	}
	if backupInfo.Size() < 16*1024 {
		t.Errorf("expected rotated backup size >= 16 KiB, got %d", backupInfo.Size())
	}

	activeInfo, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("stat active log file: %v", err)
	}
	if activeInfo.Size() >= 18*1024 {
		t.Errorf("expected active log file < 18 KiB after rotation, got %d", activeInfo.Size())
	}

	// Trigger a second rotation to verify overwriting existing .1 backup succeeds.
	for i := 120; i < 240; i++ {
		log.Add(fmt.Sprintf("entry-%03d %s", i, payload), "GPU", "info")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("expected backup after second rotation: %v", err)
	}
	activeInfo2, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("stat active log file after second rotation: %v", err)
	}
	if activeInfo2.Size() >= 18*1024 {
		t.Errorf("expected active log file < 18 KiB after second rotation, got %d", activeInfo2.Size())
	}
}

func TestSafeMessageParityCases(t *testing.T) {
	apiKey := "co_" + strings.Repeat("k", 48)
	tunnelToken := base64.StdEncoding.EncodeToString(
		[]byte(`{"a":"account","t":"tunnel","s":"secret"}`),
	)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "quoted_urls_ws_and_refresh_tokens",
			in: `POST 'https://colab.example.com/tun/m/assign?authuser=0' failed; ` +
				`ws="wss://proxy.example.com/api/kernels/123/channels" ` +
				`"token": "ya29.super-secret-value" refresh=1//09AbCdEfGhIjKlMnOpQrStUvWxYz0123456789`,
			want: `POST '[链接已隐藏]' failed; ws="[链接已隐藏]" "token": "[已隐藏]" refresh=[凭据已隐藏]`,
		},
		{
			name: "socks5_and_socks5h_urls",
			in:   "proxy failed via socks5h://alice:supersecret@10.0.0.2:1080 and socks5://u:p@1.2.3.4:1080",
			want: "proxy failed via [链接已隐藏] and [链接已隐藏]",
		},
		{
			name: "parenthesized_url_with_period",
			in:   "see (https://example.com/path?a=1).",
			want: "see ([链接已隐藏]).",
		},
		{
			name: "bracketed_http_url_with_semicolon",
			in:   "see [http://example.com/foo]; next",
			want: "see [[链接已隐藏]]; next",
		},
		{
			name: "angle_bracketed_ws_url_with_comma",
			in:   "url: <ws://127.0.0.1:8080/ws>, end",
			want: "url: <[链接已隐藏]>, end",
		},
		{
			name: "trailing_punctuation_cluster",
			in:   "trailing punct https://example.com/a:;,.",
			want: "trailing punct [链接已隐藏]:;,.",
		},
		{
			name: "dots_only_body",
			in:   "dots only http://...",
			want: "dots only [链接已隐藏]..",
		},
		{
			name: "single_colon_body",
			in:   "single colon http://:",
			want: "single colon [链接已隐藏]",
		},
		{
			name: "empty_scheme_not_redacted",
			in:   "empty scheme http:// ",
			want: "empty scheme http:// ",
		},
		{
			name: "url_stops_at_close_paren",
			in:   "http://example.com/a(b)c",
			want: "[链接已隐藏])c",
		},
		{
			name: "url_with_cjk_body_and_trailing_colon",
			in:   "http://中:",
			want: "[链接已隐藏]:",
		},
		{
			name: "url_followed_by_unicode_ideographic_space",
			in:   "中文前缀 https://example.com/path\u3000中文后缀",
			want: "中文前缀 [链接已隐藏]\u3000中文后缀",
		},
		{
			name: "bearer_mixed_case_and_delimiters",
			in:   "Bearer tok123, bearer   tok456}end BEARER\ttok789;ok",
			want: "Bearer [已隐藏], Bearer [已隐藏]}end Bearer [已隐藏];ok",
		},
		{
			name: "all_credential_patterns_at_min_length",
			in: "ya29.abc-123.def 1//abcdefghijkl hf_123-abc " +
				"co_" + strings.Repeat("k", 40) + " " +
				"eyJ" + strings.Repeat("a", 20) + " " +
				"4/abcdefghijkl%2F",
			want: "[凭据已隐藏] [凭据已隐藏] [凭据已隐藏] [凭据已隐藏] [凭据已隐藏] [凭据已隐藏]",
		},
		{
			name: "credential_patterns_below_min_length_untouched",
			in: "short tokens: 1//abcdefghijk " +
				"co_" + strings.Repeat("k", 39) + " " +
				"eyJ" + strings.Repeat("a", 19) + " " +
				"4/abcdefghijk",
			want: "short tokens: 1//abcdefghijk " +
				"co_" + strings.Repeat("k", 39) + " " +
				"eyJ" + strings.Repeat("a", 19) + " " +
				"4/abcdefghijk",
		},
		{
			name: "public_api_key_and_tunnel_token",
			in:   apiKey + " " + tunnelToken,
			want: "[凭据已隐藏] [凭据已隐藏]",
		},
		{
			name: "key_value_secrets_json_and_query",
			in:   `{"access_token": "secret1", "refresh_token": "secret2", "client_secret": "secret3", "code": "secret4", "token": "secret5"}`,
			want: `{"access_token": "[已隐藏]", "refresh_token": "[已隐藏]", "client_secret": "[已隐藏]", "code": "[已隐藏]", "token": "[已隐藏]"}`,
		},
		{
			name: "key_value_secrets_uppercase_and_single_quotes",
			in:   `ACCESS_TOKEN=abc123; 'Client_Secret' : 'xyz789'`,
			want: `ACCESS_TOKEN=[已隐藏]; 'Client_Secret' : '[已隐藏]'`,
		},
		{
			name: "plain_chinese_message_preserved",
			in:   "远端管理服务健康检查通过（耗时 1.5 秒）",
			want: "远端管理服务健康检查通过（耗时 1.5 秒）",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			if branded := ColabOneSafeMessage(tc.in); branded != tc.want {
				t.Errorf("ColabOneSafeMessage(%q):\n got: %q\nwant: %q", tc.in, branded, tc.want)
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage is not idempotent for %q:\n first: %q\nsecond: %q", tc.in, got, again)
			}
		})
	}
}

func TestSafeMessageSecurityBypassRegressions(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		forbidden []string
	}{
		{
			name:      "hf_token_json_ordering_redacts_value",
			in:        `{"hf_token":"opaque_secret_12345"}`,
			want:      `{"[凭据已隐藏]":"[已隐藏]"}`,
			forbidden: []string{"opaque_secret_12345"},
		},
		{
			name:      "hf_token_assignment_and_hf_value",
			in:        `hf_token=opaque_secret_12345 {"hf_token": "hf_realSecret98765"}`,
			want:      `[凭据已隐藏]=[已隐藏] {"[凭据已隐藏]": "[已隐藏]"}`,
			forbidden: []string{"opaque_secret_12345", "hf_realSecret98765"},
		},
		{
			name:      "hf_token_uppercase_key",
			in:        `{"HF_TOKEN":"opaque_secret_12345"}`,
			want:      `{"HF_TOKEN":"[已隐藏]"}`,
			forbidden: []string{"opaque_secret_12345"},
		},
		{
			name:      "uppercase_and_mixed_case_url_schemes",
			in:        "SOCKS5://alice:SuperSecret99@us.proxy.test:1080 SOCKS5H://bob:Pass88@eu.proxy.test:1080 HTTPS://colab.example.com/tun/m/assign?xsrf=SECRET Http://u:p@example.com/a WSS://ws.example.com/ch?token=TOK WS://127.0.0.1:8080/ws?k=V",
			want:      "[链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏]",
			forbidden: []string{"SuperSecret99", "Pass88", "us.proxy.test", "SECRET", "TOK"},
		},
		{
			name:      "socks4_and_socks4a_schemes_case_insensitive",
			in:        "proxy socks4://alice:S4Secret@10.0.0.1:1080 SOCKS4://bob:S4Upper@10.0.0.2:1080 socks4a://carol:S4aSecret@proxy.test:1080 SOCKS4A://dave:S4aUpper@proxy.test:1080",
			want:      "proxy [链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏]",
			forbidden: []string{"S4Secret", "S4Upper", "S4aSecret", "S4aUpper", "proxy.test"},
		},
		{
			name:      "ipv6_url_with_port_and_query_secret",
			in:        "https://[2001:db8::1]:8443/tun/m/assign?xsrf=SECRET",
			want:      "[链接已隐藏]",
			forbidden: []string{"2001:db8::1", "8443", "assign", "SECRET"},
		},
		{
			name:      "ipv6_url_variants_loopback_mapped_zone_and_userinfo",
			in:        "https://[::1]:8443/a?k=S1 http://[2001:db8::1] http://[::ffff:192.0.2.128]:8080/p?k=S2 https://[fe80::1%25eth0]:8443/p?xsrf=S3 https://[fe80::1%eth0]:8443/p?xsrf=S4 SOCKS5://alice:SuperSecret99@[2001:db8::1]:1080 SOCKS5H://alice:p@ss@[2001:DB8::1]:1080/proxy",
			want:      "[链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏] [链接已隐藏]",
			forbidden: []string{"S1", "S2", "S3", "S4", "SuperSecret99", "p@ss", "8443", "1080"},
		},
		{
			name:      "ipv6_url_preserves_outer_brackets_parens_angles_and_trailing_punct",
			in:        "see [https://[2001:db8::1]:8443/tun/m/assign?xsrf=SECRET]; [https://[2001:db8::1]]; (https://[2001:db8::1]:8443/a). <wss://[2001:db8::1]:8443/ws?t=SECRET>, POST https://[2001:db8::1]:8443/assign?xsrf=SECRET: fail; POST https://[2001:db8::1]: fail",
			want:      "see [[链接已隐藏]]; [[链接已隐藏]]; ([链接已隐藏]). <[链接已隐藏]>, POST [链接已隐藏]: fail; POST [链接已隐藏]: fail",
			forbidden: []string{"2001:db8::1", "8443", "SECRET"},
		},
		{
			name:      "three_part_jwt_short_header",
			in:        "token eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.sig",
			want:      "token [凭据已隐藏]",
			forbidden: []string{"eyJhbGciOiJSUzI1NiJ9", "eyJzdWIiOiIxMjMifQ", ".sig"},
		},
		{
			name:      "three_part_jwt_long_header_redacts_signature_too",
			in:        "jwt eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkFsaWNlIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c.",
			want:      "jwt [凭据已隐藏].",
			forbidden: []string{"eyJhbGci", "eyJzdWI", "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
		},
		{
			name:      "multiple_three_part_jwts_with_padding_and_urlsafe_chars",
			in:        "a=eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIn0.sig-1_a= b=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIyIn0.sig+2/b==",
			want:      "a=[凭据已隐藏] b=[凭据已隐藏]",
			forbidden: []string{"sig-1_a", "sig+2/b"},
		},
		{
			name:      "basic_auth_headers_and_go_map_formatting",
			in:        `Authorization: Basic dXNlcjpwYXNz; Proxy-Authorization: Basic YWxpY2U6U3VwZXJTZWNyZXQ5OQ==; authorization: basic c2VjcmV0MQ==; "Proxy-Authorization": "Basic c2VjcmV0Mg=="; map[Proxy-Authorization:[Basic c2VjcmV0Mw==]]; proxy_authorization=Basic c2VjcmV0NA==`,
			want:      `Authorization: Basic [已隐藏]; Proxy-Authorization: Basic [已隐藏]; authorization: Basic [已隐藏]; "Proxy-Authorization": "Basic [已隐藏]"; map[Proxy-Authorization:[Basic [已隐藏]]]; proxy_authorization=Basic [已隐藏]`,
			forbidden: []string{"dXNlcjpwYXNz", "YWxpY2U6U3VwZXJTZWNyZXQ5OQ==", "c2VjcmV0MQ==", "c2VjcmV0Mg==", "c2VjcmV0Mw==", "c2VjcmV0NA=="},
		},
		{
			name: "basic_non_credential_prose_and_challenge_preserved",
			in:   `HTTP/1.1 407 Proxy-Authenticate: Basic realm="test" | Basic auth failed`,
			want: `HTTP/1.1 407 Proxy-Authenticate: Basic realm="test" | Basic auth failed`,
		},
		{
			name:      "additional_key_value_secrets_json_and_assignments",
			in:        `{"api_key":"secret_api_1","password":"secret_pw_2","passwd":"secret_pw_3","proxy_password":"secret_pw_4","tunnel_token":"secret_tun_5"} api_key=k1 password=p1 passwd=p2 proxy_password=p3 tunnel_token=t1`,
			want:      `{"api_key":"[已隐藏]","password":"[已隐藏]","passwd":"[已隐藏]","proxy_password":"[已隐藏]","tunnel_token":"[已隐藏]"} api_key=[已隐藏] password=[已隐藏] passwd=[已隐藏] proxy_password=[已隐藏] tunnel_token=[已隐藏]`,
			forbidden: []string{"secret_api_1", "secret_pw_2", "secret_pw_3", "secret_pw_4", "secret_tun_5", "=k1", "=p1", "=p2", "=p3", "=t1"},
		},
		{
			name:      "quoted_passwords_with_special_chars_and_empty_value",
			in:        `{"password": "my secret, with; } chars", "proxy_password": "o'malley", "passwd": "abc\"def", "api_key": "", "ok": true}`,
			want:      `{"password": "[已隐藏]", "proxy_password": "[已隐藏]", "passwd": "[已隐藏]", "api_key": "", "ok": true}`,
			forbidden: []string{"my secret", "with;", "chars", "malley", "abc", "def"},
		},
		{
			name:      "colab_runtime_proxy_token_plain_percent_encoded_and_header",
			in:        `colab-runtime-proxy-token=tokPlain1 colab-runtime-proxy-token%3dtokLower2 redirect https%3A%2F%2Frt.invalid%3Fcolab-runtime-proxy-token%3DtokUpper3%26content%3D1 failed; X-Colab-Runtime-Proxy-Token: tokHeader4; {"colab-runtime-proxy-token": "tokJson5"}`,
			want:      `colab-runtime-proxy-token=[已隐藏] colab-runtime-proxy-token%3d[已隐藏] redirect https%3A%2F%2Frt.invalid%3Fcolab-runtime-proxy-token%3D[已隐藏] failed; X-Colab-Runtime-Proxy-Token: [已隐藏]; {"colab-runtime-proxy-token": "[已隐藏]"}`,
			forbidden: []string{"tokPlain1", "tokLower2", "tokUpper3", "tokHeader4", "tokJson5"},
		},
		{
			name:      "escaped_quotes_and_backslash_values",
			in:        `failed parsing "{\"hf_token\":\"opaque_secret_12345\",\"password\":\"SuperSecret99\",\"token\":\"\"}" header \"Proxy-Authorization\": \"Basic c2VjcmV0NQ==\" password=foo\bar`,
			want:      `failed parsing "{\"[凭据已隐藏]\":\"[已隐藏]\",\"password\":\"[已隐藏]\",\"token\":\"\"}" header \"Proxy-Authorization\": \"Basic [已隐藏]\" password=[已隐藏]`,
			forbidden: []string{"opaque_secret_12345", "SuperSecret99", "c2VjcmV0NQ==", `foo\bar`},
		},
		{
			name:      "basic_auth_json_header_arrays",
			in:        `{"Authorization":["Basic dXNlcjpwYXNz"],"Proxy-Authorization":["Basic YWxpY2U6cGFzcw=="]} {"Authorization": [ "Basic c2VjcmV0Ng==" ]}`,
			want:      `{"Authorization":["Basic [已隐藏]"],"Proxy-Authorization":["Basic [已隐藏]"]} {"Authorization": [ "Basic [已隐藏]" ]}`,
			forbidden: []string{"dXNlcjpwYXNz", "YWxpY2U6cGFzcw==", "c2VjcmV0Ng=="},
		},
		{
			name:      "basic_auth_escaped_json_header_arrays",
			in:        `"{\"Proxy-Authorization\":[\"Basic YWxpY2U6cGFzcw==\"]}" ` + fmt.Sprintf("%q", `{"Authorization":["Basic dXNlcjpwYXNz"],"Proxy-Authorization":["Basic c2VjcmV0Nw=="]}`),
			want:      `"{\"Proxy-Authorization\":[\"Basic [已隐藏]\"]}" "{\"Authorization\":[\"Basic [已隐藏]\"],\"Proxy-Authorization\":[\"Basic [已隐藏]\"]}"`,
			forbidden: []string{"YWxpY2U6cGFzcw==", "dXNlcjpwYXNz", "c2VjcmV0Nw=="},
		},
		{
			name:      "basic_auth_python_dict_header_arrays",
			in:        `{'Authorization': ['Basic dXNlcjpwYXNz'], 'Proxy-Authorization': [ 'Basic YWxpY2U6cGFzcw==' ]}`,
			want:      `{'Authorization': ['Basic [已隐藏]'], 'Proxy-Authorization': [ 'Basic [已隐藏]' ]}`,
			forbidden: []string{"dXNlcjpwYXNz", "YWxpY2U6cGFzcw=="},
		},
		{
			name: "basic_auth_json_header_array_non_credential_preserved",
			in:   `{"Proxy-Authenticate":["Basic realm=\"test\""],"WWW-Authenticate":["Basic realm=\"colab\""]}`,
			want: `{"Proxy-Authenticate":["Basic realm=\"test\""],"WWW-Authenticate":["Basic realm=\"colab\""]}`,
		},
		{
			name:      "escaped_double_quote_backslash_and_nested_quote_secrets",
			in:        `\"password\":\"foo\\\\bar\" \"password\":\"a\\\"SuperSecret99\" \"password\":\"\\\\SuperSecret99\" \"password\":\"foo\\\\\" \"password\":\"\\LeadingSecret\"`,
			want:      `\"password\":\"[已隐藏]\" \"password\":\"[已隐藏]\" \"password\":\"[已隐藏]\" \"password\":\"[已隐藏]\" \"password\":\"[已隐藏]\"`,
			forbidden: []string{"foo", "bar", "SuperSecret99", "LeadingSecret"},
		},
		{
			name:      "go_percent_q_json_in_json_escaped_secrets",
			in:        fmt.Sprintf("%q", `{"password":"foo\\bar","passwd":"a\"SuperSecret99","proxy_password":"\\SuperSecret99","api_key":"foo\\","token":""}`),
			want:      `"{\"password\":\"[已隐藏]\",\"passwd\":\"[已隐藏]\",\"proxy_password\":\"[已隐藏]\",\"api_key\":\"[已隐藏]\",\"token\":\"\"}"`,
			forbidden: []string{"foo", "bar", "SuperSecret99"},
		},
		{
			name:      "escaped_single_quote_secrets_and_backslashes",
			in:        `password=\'SuperSecret\' passwd=\'foo\\\\bar\' proxy_password=\'a\\\'SuperSecret99\' api_key=\'\\\\SuperSecret88\' \'token\': \'my secret, with; } chars\'`,
			want:      `password=\'[已隐藏]\' passwd=\'[已隐藏]\' proxy_password=\'[已隐藏]\' api_key=\'[已隐藏]\' \'token\': \'[已隐藏]\'`,
			forbidden: []string{"SuperSecret", "SuperSecret99", "SuperSecret88", "foo", "bar", "my secret"},
		},
		{
			name: "empty_escaped_double_and_single_quotes_not_overmatched",
			in:   `\"password\":\"\" \"token\": \"\" password=\'\' \'api_key\': \'\' ok=true`,
			want: `\"password\":\"\" \"token\": \"\" password=\'\' \'api_key\': \'\' ok=true`,
		},
		{
			name:      "empty_escaped_quotes_followed_by_real_secrets",
			in:        `"{\"token\":\"\",\"password\":\"SuperSecret99\"}" password=\'\', api_key=\'SuperSecret88\'`,
			want:      `"{\"token\":\"\",\"password\":\"[已隐藏]\"}" password=\'\', api_key=\'[已隐藏]\'`,
			forbidden: []string{"SuperSecret99", "SuperSecret88"},
		},
	}

	// Also test real json.Marshal(http.Header), %q-formatted marshaled http.Header,
	// and Go %#v / %v formatting for Basic auth and key-value header slices.
	hdrBytes, err := json.Marshal(http.Header{
		"Authorization":       {"Basic dXNlcjpwYXNz"},
		"Proxy-Authorization": {"Basic YWxpY2U6cGFzcw=="},
		"Proxy-Authenticate":  {`Basic realm="test"`},
	})
	if err != nil {
		t.Fatalf("marshal http.Header: %v", err)
	}
	hdrJSON := string(hdrBytes)

	proxyHdrBytes, err := json.Marshal(http.Header{
		"X-Colab-Runtime-Proxy-Token": {"my-proxy-secret"},
	})
	if err != nil {
		t.Fatalf("marshal proxy http.Header: %v", err)
	}
	apiKeyHdrBytes, err := json.Marshal(http.Header{
		"api_key": {"sk-secret"},
	})
	if err != nil {
		t.Fatalf("marshal api_key http.Header: %v", err)
	}

	cases = append(cases,
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "marshaled_http_header_basic_auth",
			in:        hdrJSON,
			want:      `{"Authorization":["Basic [已隐藏]"],"Proxy-Authenticate":["Basic realm=\"test\""],"Proxy-Authorization":["Basic [已隐藏]"]}`,
			forbidden: []string{"dXNlcjpwYXNz", "YWxpY2U6cGFzcw=="},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "percent_q_marshaled_http_header_basic_auth",
			in:        fmt.Sprintf("%q", hdrJSON),
			want:      `"{\"Authorization\":[\"Basic [已隐藏]\"],\"Proxy-Authenticate\":[\"Basic realm=\\\"test\\\"\"],\"Proxy-Authorization\":[\"Basic [已隐藏]\"]}"`,
			forbidden: []string{"dXNlcjpwYXNz", "YWxpY2U6cGFzcw=="},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "marshaled_http_header_colab_runtime_proxy_token",
			in:        string(proxyHdrBytes),
			want:      `{"X-Colab-Runtime-Proxy-Token":["[已隐藏]"]}`,
			forbidden: []string{"my-proxy-secret"},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "marshaled_http_header_api_key",
			in:        string(apiKeyHdrBytes),
			want:      `{"api_key":["[已隐藏]"]}`,
			forbidden: []string{"sk-secret"},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "escaped_json_array_key_value_secrets",
			in:        `\"token\":[\"secret\"] \"api_key\": [ \"sk-nested\\\"secret\" ] ` + fmt.Sprintf("%q", string(proxyHdrBytes)),
			want:      `\"token\":[\"[已隐藏]\"] \"api_key\": [ \"[已隐藏]\" ] "{\"X-Colab-Runtime-Proxy-Token\":[\"[已隐藏]\"]}"`,
			forbidden: []string{"secret", "sk-nested", "my-proxy-secret"},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "go_syntax_http_header_basic_auth",
			in:        fmt.Sprintf("%#v", http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}}),
			want:      `http.Header{"Authorization":[]string{"Basic [已隐藏]"}}`,
			forbidden: []string{"dXNlcjpwYXNz"},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "go_syntax_http_header_proxy_auth_and_non_credential_challenge",
			in:        fmt.Sprintf("%#v %#v", http.Header{"Proxy-Authorization": {"Basic YWxpY2U6cGFzcw=="}}, http.Header{"Proxy-Authenticate": {`Basic realm="test"`}}),
			want:      `http.Header{"Proxy-Authorization":[]string{"Basic [已隐藏]"}} http.Header{"Proxy-Authenticate":[]string{"Basic realm=\"test\""}}`,
			forbidden: []string{"YWxpY2U6cGFzcw=="},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "go_syntax_http_header_colab_runtime_proxy_token_and_api_key",
			in:        fmt.Sprintf("%#v %#v", http.Header{"X-Colab-Runtime-Proxy-Token": {"my-proxy-secret"}}, http.Header{"api_key": {"sk-secret"}}),
			want:      `http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"[已隐藏]"}} http.Header{"api_key":[]string{"[已隐藏]"}}`,
			forbidden: []string{"my-proxy-secret", "sk-secret"},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name:      "go_percent_v_http_header_colab_runtime_proxy_token",
			in:        fmt.Sprintf("%v", http.Header{"X-Colab-Runtime-Proxy-Token": {"my-proxy-secret"}}),
			want:      `map[X-Colab-Runtime-Proxy-Token:[[已隐藏]]]`,
			forbidden: []string{"my-proxy-secret"},
		},
		struct {
			name      string
			in        string
			want      string
			forbidden []string
		}{
			name: "empty_json_array_and_go_slice_values_not_corrupted",
			in:   `{"token":[]} {"api_key":[""]} http.Header{"token":[]string{}} http.Header{"api_key":[]string{""}}`,
			want: `{"token":[]} {"api_key":[""]} http.Header{"token":[]string{}} http.Header{"api_key":[]string{""}}`,
		},
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Errorf("SafeMessage(%q) leaked secret %q in %q", tc.in, secret, got)
				}
			}
			if branded := ColabOneSafeMessage(tc.in); branded != tc.want {
				t.Errorf("ColabOneSafeMessage(%q):\n got: %q\nwant: %q", tc.in, branded, tc.want)
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage is not idempotent for %q:\n first: %q\nsecond: %q", tc.in, got, again)
			}
		})
	}

	// Verify none of the new bypass vectors leak into in-memory snapshot or disk log.
	logFile := filepath.Join(t.TempDir(), "bypass.log")
	log := NewColabOneActivityLog(10, logFile, 16*1024)
	defer log.Close()

	log.Add(`{"hf_token":"opaque_secret_12345"} SOCKS5://alice:SuperSecret99@us.proxy.test:1080`, "代理", "error")
	log.Add(`https://[2001:db8::1]:8443/tun/m/assign?xsrf=XSRF_SECRET eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.jwt_sig`, "远端", "error")
	log.Add(`Proxy-Authorization: Basic YWxpY2U6U3VwZXJTZWNyZXQ5OQ== colab-runtime-proxy-token%3DPROXY_TOK_SECRET`, "API", "warn")
	log.Add(`{"Authorization":["Basic dXNlcjpwYXNz"],"Proxy-Authorization":["Basic cGFzc19qc29u"]} {\"password\":\"foo\\\\barSecret\"} password=\'SingleEscSecret\'`, "代理", "error")

	snapBytes, err := json.Marshal(log.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	diskBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read bypass.log: %v", err)
	}
	combined := string(snapBytes) + "\n" + string(diskBytes)
	for _, secret := range []string{
		"opaque_secret_12345",
		"SuperSecret99",
		"us.proxy.test",
		"XSRF_SECRET",
		"jwt_sig",
		"YWxpY2U6U3VwZXJTZWNyZXQ5OQ==",
		"PROXY_TOK_SECRET",
		"dXNlcjpwYXNz",
		"cGFzc19qc29u",
		"barSecret",
		"SingleEscSecret",
	} {
		if strings.Contains(combined, secret) {
			t.Errorf("secret %q leaked into snapshot or disk log: %s", secret, combined)
		}
	}
}

func TestSafeMessageRedactsRTMPURLsAndBareSkKeys(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		forbidden []string
	}{
		{
			name:      "rtmp_stream_key_redacted",
			in:        "FFmpeg push failed for rtmp://host/live/secret_key: connection reset",
			want:      "FFmpeg push failed for [链接已隐藏]: connection reset",
			forbidden: []string{"host", "secret_key"},
		},
		{
			name:      "rtmps_credentials_path_and_query_redacted",
			in:        "push rtmps://user:streamPass@ingest.example.com:1935/live2/my_secret_stream?token=tok123 failed",
			want:      "push [链接已隐藏] failed",
			forbidden: []string{"streamPass", "ingest.example.com", "my_secret_stream", "tok123"},
		},
		{
			name:      "uppercase_rtmp_ipv6_redacted",
			in:        "RTMP://[2001:db8::1]:1935/live/secret_ipv6_key.",
			want:      "[链接已隐藏].",
			forbidden: []string{"2001:db8::1", "secret_ipv6_key"},
		},
		{
			name:      "bare_sk_openai_style_tokens_redacted",
			in:        "external LLM error for sk-proj-secret123456 and (sk-ant-api03_abcdef123456).",
			want:      "external LLM error for [凭据已隐藏] and ([凭据已隐藏]).",
			forbidden: []string{"sk-proj-secret123456", "sk-ant-api03_abcdef123456"},
		},
		{
			name: "short_sk_and_midword_not_overredacted",
			in:   "label disk-123456789012 short sk-123456789 ok",
			want: "label disk-123456789012 short sk-123456789 ok",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Errorf("SafeMessage(%q) leaked %q in %q", tc.in, secret, got)
				}
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage not idempotent: first=%q second=%q", got, again)
			}
		})
	}
}

func TestSafeMessageTruncationUTF8SafeAndAfterRedaction(t *testing.T) {
	// 1. ASCII truncation to 600 runes.
	asciiIn := strings.Repeat("a", 650)
	asciiOut := SafeMessage(asciiIn)
	if utf8.RuneCountInString(asciiOut) != 600 {
		t.Fatalf("expected 600 ASCII runes, got %d", utf8.RuneCountInString(asciiOut))
	}

	// 2. Multi-byte Chinese characters truncated cleanly at 600 runes (1800 bytes).
	cjkIn := strings.Repeat("中", 650)
	cjkOut := SafeMessage(cjkIn)
	if !utf8.ValidString(cjkOut) {
		t.Fatal("truncated Chinese string is not valid UTF-8")
	}
	if utf8.RuneCountInString(cjkOut) != 600 {
		t.Fatalf("expected 600 Chinese runes, got %d", utf8.RuneCountInString(cjkOut))
	}

	// 3. 4-byte emoji truncated cleanly at 600 runes.
	emojiIn := strings.Repeat("🚀", 620)
	emojiOut := SafeMessage(emojiIn)
	if !utf8.ValidString(emojiOut) {
		t.Fatal("truncated emoji string is not valid UTF-8")
	}
	if utf8.RuneCountInString(emojiOut) != 600 {
		t.Fatalf("expected 600 emoji runes, got %d", utf8.RuneCountInString(emojiOut))
	}

	// 4. Secret near/past rune 595 must still be redacted before truncation.
	nearBoundary := strings.Repeat("x", 596) + "https://secret.example.com/very-long-secret-path"
	out := SafeMessage(nearBoundary)
	if strings.Contains(out, "secret.example.com") || strings.Contains(out, "http") {
		t.Fatalf("URL past rune boundary leaked: %q", out)
	}
	if utf8.RuneCountInString(out) != 600 {
		t.Fatalf("expected 600 runes after redaction+truncation, got %d", utf8.RuneCountInString(out))
	}
	if !utf8.ValidString(out) {
		t.Fatal("output after redaction+truncation is not valid UTF-8")
	}
}

func TestActivityDefaultsSanitizationAndJSONFields(t *testing.T) {
	fixedTime := time.Date(2026, 4, 5, 12, 34, 56, 500_000_000, time.Local)

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "nested", "dir", "activity.log")
	log := NewColabOneActivityLog(0, logPath, 1024) // limit defaults to 300; maxBytes clamps to 16 KiB
	defer log.Close()

	if log.limit != DefaultLimit {
		t.Fatalf("expected default limit %d, got %d", DefaultLimit, log.limit)
	}
	if log.maxBytes != MinMaxBytes {
		t.Fatalf("expected clamped maxBytes %d, got %d", MinMaxBytes, log.maxBytes)
	}

	defaultMaxLog := NewColabOneActivityLog(-1, "", 0)
	if defaultMaxLog.limit != DefaultLimit || defaultMaxLog.maxBytes != DefaultMaxBytes {
		t.Fatalf("unexpected defaults: limit=%d maxBytes=%d", defaultMaxLog.limit, defaultMaxLog.maxBytes)
	}

	log.nowFn = func() time.Time { return fixedTime }

	// Long Chinese source (>40 runes), long uppercase level (>16 runes), multiline message.
	log.Add(
		"line1\r\nline2\nline3",
		strings.Repeat("云", 50),
		"SUPERLONGLEVELNAME12345",
	)
	// Credential inside source must also be redacted before truncation.
	log.Add("ok", "https://secret.example.com/a/b", "")

	snap := log.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap))
	}

	first := snap[0]
	if first.ID != 1 {
		t.Errorf("expected ID 1, got %d", first.ID)
	}
	if math.IsNaN(first.Time) || math.IsInf(first.Time, 0) {
		t.Errorf("entry time must be finite, got %v", first.Time)
	}
	if math.Abs(first.Time-float64(fixedTime.UnixNano())/1e9) > 1e-6 {
		t.Errorf("unexpected entry time: got %f, want %f", first.Time, float64(fixedTime.UnixNano())/1e9)
	}
	if utf8.RuneCountInString(first.Source) != 40 || !utf8.ValidString(first.Source) {
		t.Errorf("expected source truncated to 40 valid UTF-8 runes, got %d (%q)", utf8.RuneCountInString(first.Source), first.Source)
	}
	if first.Level != "superlonglevelna" {
		t.Errorf("expected lowercased 16-rune level 'superlonglevelna', got %q", first.Level)
	}
	if first.Message != "line1\r\nline2\nline3" {
		t.Errorf("expected in-memory message to preserve newlines, got %q", first.Message)
	}

	second := snap[1]
	if second.Source != "[链接已隐藏]" {
		t.Errorf("expected redacted source '[链接已隐藏]', got %q", second.Source)
	}
	if second.Level != "info" {
		t.Errorf("expected default level 'info', got %q", second.Level)
	}

	// Verify exact serialized JSON field contract for ColabOneActivityEntry.
	var decoded []map[string]any
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	expectedKeys := []string{"id", "time", "source", "level", "message"}
	for _, k := range expectedKeys {
		if _, ok := decoded[0][k]; !ok {
			t.Errorf("missing expected JSON key %q in %v", k, decoded[0])
		}
	}
	if len(decoded[0]) != len(expectedKeys) {
		t.Errorf("unexpected extra JSON keys: %v", decoded[0])
	}

	// Verify disk line formatting (YYYY-MM-DD HH:MM:SS [LEVEL] [source] message\n)
	// and \r / \n replacement with spaces.
	diskBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read disk log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(diskBytes), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 disk log lines, got %d: %q", len(lines), string(diskBytes))
	}
	wantLine0 := fmt.Sprintf("2026-04-05 12:34:56 [SUPERLONGLEVELNA] [%s] line1  line2 line3", strings.Repeat("云", 40))
	if lines[0] != wantLine0 {
		t.Errorf("disk line 0 mismatch:\n got: %q\nwant: %q", lines[0], wantLine0)
	}
	wantLine1 := "2026-04-05 12:34:56 [INFO] [[链接已隐藏]] ok"
	if lines[1] != wantLine1 {
		t.Errorf("disk line 1 mismatch:\n got: %q\nwant: %q", lines[1], wantLine1)
	}

	// Verify Snapshot returns a defensive copy.
	snap[0].Message = "mutated"
	if log.Snapshot()[0].Message == "mutated" {
		t.Fatal("mutating Snapshot slice affected internal ring buffer")
	}
}

func TestActivityRingBufferWrapAround(t *testing.T) {
	for _, limit := range []int{1, 2, 3, 5} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			log := NewColabOneActivityLog(limit, "", 0)
			total := limit*3 + 2
			for i := 1; i <= total; i++ {
				log.Add(fmt.Sprintf("msg-%d", i), "GPU", "info")
				snap := log.Snapshot()
				wantLen := min(i, limit)
				if len(snap) != wantLen {
					t.Fatalf("step %d: len=%d, want %d", i, len(snap), wantLen)
				}
				startID := i - wantLen + 1
				for j, entry := range snap {
					wantID := startID + j
					if entry.ID != wantID {
						t.Fatalf("step %d idx %d: ID=%d, want %d", i, j, entry.ID, wantID)
					}
					wantMsg := fmt.Sprintf("msg-%d", wantID)
					if entry.Message != wantMsg {
						t.Fatalf("step %d idx %d: msg=%q, want %q", i, j, entry.Message, wantMsg)
					}
				}
			}
		})
	}
}

func TestActivityCloseAndEdgeSafety(t *testing.T) {
	// Nil receiver must not panic.
	var nilLog *ColabOneActivityLog
	nilLog.Add("ignored", "GPU", "info")
	if snap := nilLog.Snapshot(); snap == nil || len(snap) != 0 {
		t.Fatalf("expected non-nil empty snapshot on nil receiver, got %#v", snap)
	}
	nilLog.Clear()
	nilLog.Close()

	// Zero-value struct must not panic.
	var zeroLog ColabOneActivityLog
	zeroLog.Add("hello", "", "")
	if snap := zeroLog.Snapshot(); len(snap) != 1 || snap[0].Message != "hello" {
		t.Fatalf("unexpected zero-value snapshot: %+v", snap)
	}
	zeroLog.Close()

	// Unwriteable disk path (e.g., pointing at an existing directory) must fail silently.
	dirAsFile := t.TempDir()
	unwriteable := NewColabOneActivityLog(5, dirAsFile, 16*1024)
	unwriteable.Add("still in memory", "客户端", "info")
	if snap := unwriteable.Snapshot(); len(snap) != 1 || snap[0].Message != "still in memory" {
		t.Fatalf("expected in-memory entry despite disk write failure, got %+v", snap)
	}
	unwriteable.Close()

	// Close stops further disk writes and is idempotent.
	logFile := filepath.Join(t.TempDir(), "close.log")
	log := NewColabOneActivityLog(5, logFile, 16*1024)
	log.Add("before close", "客户端", "info")
	log.Close()
	log.Close()
	log.Add("after close", "客户端", "info")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read close.log: %v", err)
	}
	if !strings.Contains(string(data), "before close") {
		t.Errorf("expected 'before close' in disk log, got %q", string(data))
	}
	if strings.Contains(string(data), "after close") {
		t.Errorf("did not expect 'after close' written to disk after Close(): %q", string(data))
	}
}

func TestActivityConcurrentAccessRace(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "race.log")
	log := NewColabOneActivityLog(32, logFile, 16*1024)

	const workers = 12
	const iters = 40
	var wg sync.WaitGroup

	// Concurrent writers.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				log.Add(
					fmt.Sprintf("worker=%d iter=%d https://example.com/?token=secret-%d-%d %s",
						workerID, i, workerID, i, strings.Repeat("a", 64)),
					"GPU",
					"info",
				)
			}
		}(w)
	}

	// Concurrent readers verifying monotonic IDs, bounded size, and redaction.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				snap := log.Snapshot()
				if len(snap) > 32 {
					t.Errorf("snapshot exceeded limit: %d", len(snap))
					return
				}
				for j := 1; j < len(snap); j++ {
					if snap[j].ID <= snap[j-1].ID {
						t.Errorf("non-monotonic IDs in snapshot: %d then %d", snap[j-1].ID, snap[j].ID)
						return
					}
				}
				for _, e := range snap {
					if strings.Contains(e.Message, "secret-") || strings.Contains(e.Message, "example.com") {
						t.Errorf("unredacted secret observed under concurrency: %q", e.Message)
						return
					}
				}
			}
		}()
	}

	// Concurrent periodic clear.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			log.Clear()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
	log.Close()

	// Final disk content must contain zero leaked secrets.
	for _, path := range []string{logFile, logFile + ".1"} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if strings.Contains(text, "secret-") || strings.Contains(text, "example.com") {
			t.Fatalf("unredacted secret leaked to disk %s", path)
		}
	}
}

func TestSafeMessageMultiElementContainersAndBearerDelimiters(t *testing.T) {
	multiHdr := http.Header{
		"Authorization":               {"Basic aaa", "Basic bbb"},
		"Proxy-Authorization":         {"", "Basic ccc"},
		"Proxy-Authenticate":          {`Basic realm="ColabOne"`},
		"X-Colab-Runtime-Proxy-Token": {"tok1", "tok2"},
		"api_key":                     {"", "sk-second"},
	}
	multiHdrJSONBytes, err := json.Marshal(multiHdr)
	if err != nil {
		t.Fatalf("marshal multiHdr: %v", err)
	}
	multiHdrJSON := string(multiHdrJSONBytes)

	bearerMultiHdr := http.Header{
		"Authorization": {"Bearer x", "Basic bbb"},
	}
	bearerMultiJSONBytes, err := json.Marshal(bearerMultiHdr)
	if err != nil {
		t.Fatalf("marshal bearerMultiHdr: %v", err)
	}

	cases := []struct {
		name      string
		in        string
		want      string
		forbidden []string
	}{
		// 1. bearerPattern delimiters (], ), }, \)
		{
			name:      "bearer_go_percent_v_map_preserves_closing_brackets",
			in:        fmt.Sprintf("%v", http.Header{"Authorization": {"Bearer secret"}}),
			want:      `map[Authorization:[Bearer [已隐藏]]]`,
			forbidden: []string{"secret"},
		},
		{
			name:      "bearer_go_percent_v_multi_element_preserves_brackets",
			in:        fmt.Sprintf("%v", http.Header{"Authorization": {"Bearer secret1", "Bearer secret2"}}),
			want:      `map[Authorization:[Bearer [已隐藏] Bearer [已隐藏]]]`,
			forbidden: []string{"secret1", "secret2"},
		},
		{
			name:      "bearer_percent_q_escaped_json_preserves_backslash_quote",
			in:        fmt.Sprintf("%q", `{"Authorization":["Bearer secret"]}`),
			want:      `"{\"Authorization\":[\"Bearer [已隐藏]\"]}"`,
			forbidden: []string{"secret"},
		},
		{
			name:      "bearer_escaped_quote_and_parens_and_backslash",
			in:        `\"Bearer secret1\" (Bearer secret2) {Bearer secret3} Bearer secret4\end`,
			want:      `\"Bearer [已隐藏]\" (Bearer [已隐藏]) {Bearer [已隐藏]} Bearer [已隐藏]\end`,
			forbidden: []string{"secret1", "secret2", "secret3", "secret4"},
		},
		{
			name:      "bearer_fake_redaction_prefix_does_not_bypass",
			in:        `Bearer [已隐藏]leaked_suffix`,
			want:      `Bearer [已隐藏]`,
			forbidden: []string{"leaked_suffix"},
		},

		// 2. Multi-element Authorization containers
		{
			name:      "json_array_authorization_two_basic_elements",
			in:        `{"Authorization":["Basic aaa", "Basic bbb"]}`,
			want:      `{"Authorization":["Basic [已隐藏]", "Basic [已隐藏]"]}`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "json_array_authorization_empty_then_basic",
			in:        `{"Authorization":["", "Basic bbb"]}`,
			want:      `{"Authorization":["", "Basic [已隐藏]"]}`,
			forbidden: []string{"bbb"},
		},
		{
			name:      "json_array_authorization_bearer_then_basic",
			in:        `{"Authorization":["Bearer x", "Basic bbb"]}`,
			want:      `{"Authorization":["Bearer [已隐藏]", "Basic [已隐藏]"]}`,
			forbidden: []string{"Bearer x", "bbb"},
		},
		{
			name:      "marshaled_http_header_bearer_then_basic",
			in:        string(bearerMultiJSONBytes),
			want:      `{"Authorization":["Bearer [已隐藏]","Basic [已隐藏]"]}`,
			forbidden: []string{"Bearer x", "bbb"},
		},
		{
			name:      "percent_q_json_array_authorization_multi_elements",
			in:        fmt.Sprintf("%q", `{"Authorization":["", "Bearer x", "Basic aaa", "Basic bbb"]}`),
			want:      `"{\"Authorization\":[\"\", \"Bearer [已隐藏]\", \"Basic [已隐藏]\", \"Basic [已隐藏]\"]}"`,
			forbidden: []string{"Bearer x", "aaa", "bbb"},
		},
		{
			name:      "go_syntax_http_header_authorization_two_basic_elements",
			in:        `http.Header{"Authorization":[]string{"Basic aaa", "Basic bbb"}}`,
			want:      `http.Header{"Authorization":[]string{"Basic [已隐藏]", "Basic [已隐藏]"}}`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "go_syntax_http_header_authorization_empty_and_bearer_then_basic",
			in:        `http.Header{"Authorization":[]string{"", "Basic bbb"}} http.Header{"Proxy-Authorization":[]string{"Bearer x", "Basic ccc"}}`,
			want:      `http.Header{"Authorization":[]string{"", "Basic [已隐藏]"}} http.Header{"Proxy-Authorization":[]string{"Bearer [已隐藏]", "Basic [已隐藏]"}}`,
			forbidden: []string{"Bearer x", "bbb", "ccc"},
		},
		{
			name:      "go_percent_v_authorization_two_basic_elements",
			in:        `map[Authorization:[Basic aaa Basic bbb]]`,
			want:      `map[Authorization:[Basic [已隐藏] Basic [已隐藏]]]`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "go_percent_v_authorization_bearer_then_basic_nested_brackets",
			in:        `map[Authorization:[Bearer x Basic bbb]]`,
			want:      `map[Authorization:[Bearer [已隐藏] Basic [已隐藏]]]`,
			forbidden: []string{"Bearer x", "bbb"},
		},
		{
			name:      "go_percent_v_authorization_empty_then_basic",
			in:        fmt.Sprintf("%v", http.Header{"Authorization": {"", "Basic bbb"}}),
			want:      `map[Authorization:[ Basic [已隐藏]]]`,
			forbidden: []string{"bbb"},
		},
		{
			name:      "python_list_authorization_empty_and_multi_basic",
			in:        `{'Authorization': ['', 'Basic aaa', 'Basic bbb']}`,
			want:      `{'Authorization': ['', 'Basic [已隐藏]', 'Basic [已隐藏]']}`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "multiline_json_array_authorization_multi_basic",
			in:        "{\n  \"Authorization\": [\n    \"\",\n    \"Basic aaa\",\n    \"Basic bbb\"\n  ]\n}",
			want:      "{\n  \"Authorization\": [\n    \"\",\n    \"Basic [已隐藏]\",\n    \"Basic [已隐藏]\"\n  ]\n}",
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "truncated_unclosed_authorization_arrays_and_slices",
			in:        `{"Authorization":["Basic aaa", "Basic bbb`,
			want:      `{"Authorization":["Basic [已隐藏]", "Basic [已隐藏]`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "truncated_unclosed_go_percent_v_authorization_slice",
			in:        `map[Authorization:[Basic aaa Basic bbb`,
			want:      `map[Authorization:[Basic [已隐藏] Basic [已隐藏]`,
			forbidden: []string{"aaa", "bbb"},
		},

		// 3. Multi-element secret-key containers
		{
			name:      "json_array_colab_runtime_proxy_token_two_elements",
			in:        `{"X-Colab-Runtime-Proxy-Token":["tok1", "tok2"]}`,
			want:      `{"X-Colab-Runtime-Proxy-Token":["[已隐藏]", "[已隐藏]"]}`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "json_array_colab_runtime_proxy_token_empty_then_secret",
			in:        `{"X-Colab-Runtime-Proxy-Token":["", "tok2"]}`,
			want:      `{"X-Colab-Runtime-Proxy-Token":["", "[已隐藏]"]}`,
			forbidden: []string{"tok2"},
		},
		{
			name:      "go_syntax_http_header_colab_runtime_proxy_token_two_elements",
			in:        `http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"tok1", "tok2"}}`,
			want:      `http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"[已隐藏]", "[已隐藏]"}}`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "go_syntax_http_header_colab_runtime_proxy_token_empty_then_secret",
			in:        `http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"", "tok2"}}`,
			want:      `http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"", "[已隐藏]"}}`,
			forbidden: []string{"tok2"},
		},
		{
			name:      "go_percent_v_colab_runtime_proxy_token_two_elements",
			in:        `map[X-Colab-Runtime-Proxy-Token:[tok1 tok2]]`,
			want:      `map[X-Colab-Runtime-Proxy-Token:[[已隐藏] [已隐藏]]]`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "go_percent_v_colab_runtime_proxy_token_empty_then_secret",
			in:        fmt.Sprintf("%v", http.Header{"X-Colab-Runtime-Proxy-Token": {"", "tok2"}}),
			want:      `map[X-Colab-Runtime-Proxy-Token:[ [已隐藏]]]`,
			forbidden: []string{"tok2"},
		},
		{
			name:      "escaped_json_array_token_empty_then_secret",
			in:        `\"token\":[\"\", \"tok2\"]`,
			want:      `\"token\":[\"\", \"[已隐藏]\"]`,
			forbidden: []string{"tok2"},
		},
		{
			name:      "escaped_json_array_api_key_nested_escapes_and_multi_elements",
			in:        fmt.Sprintf("%q", `{"api_key":["","sk-1\"quoted","sk-2\\end"],"X-Colab-Runtime-Proxy-Token":["tok1","tok2"]}`),
			want:      `"{\"api_key\":[\"\",\"[已隐藏]\",\"[已隐藏]\"],\"X-Colab-Runtime-Proxy-Token\":[\"[已隐藏]\",\"[已隐藏]\"]}"`,
			forbidden: []string{"sk-1", "quoted", "sk-2", "tok1", "tok2"},
		},
		{
			name:      "percent_q_wrapped_go_syntax_http_header_multi_secrets",
			in:        fmt.Sprintf("%q", fmt.Sprintf("%#v", http.Header{"X-Colab-Runtime-Proxy-Token": {"tok1", "tok2"}})),
			want:      `"http.Header{\"X-Colab-Runtime-Proxy-Token\":[]string{\"[已隐藏]\", \"[已隐藏]\"}}"`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "json_array_secret_elements_containing_brackets_braces_and_quotes",
			in:        `{"password":["a]b","c}d","e\"f","g\\h"],"ok":true}`,
			want:      `{"password":["[已隐藏]","[已隐藏]","[已隐藏]","[已隐藏]"],"ok":true}`,
			forbidden: []string{"a]b", "c}d", "e\\\"f", "g\\\\h"},
		},
		{
			name:      "multiline_json_array_secret_multi_elements",
			in:        "{\n  \"X-Colab-Runtime-Proxy-Token\": [\n    \"\",\n    \"tok1\",\n    \"tok2\"\n  ]\n}",
			want:      "{\n  \"X-Colab-Runtime-Proxy-Token\": [\n    \"\",\n    \"[已隐藏]\",\n    \"[已隐藏]\"\n  ]\n}",
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "truncated_unclosed_secret_json_array",
			in:        `{"X-Colab-Runtime-Proxy-Token":["tok1", "tok2`,
			want:      `{"X-Colab-Runtime-Proxy-Token":["[已隐藏]", "[已隐藏]`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "truncated_unclosed_secret_go_slice",
			in:        `map[X-Colab-Runtime-Proxy-Token:[tok1 tok2`,
			want:      `map[X-Colab-Runtime-Proxy-Token:[[已隐藏] [已隐藏]`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "quoted_scalar_containing_unclosed_secret_bracket_does_not_corrupt_siblings",
			in:        `{"password": "my token=[abc", "arr": [1, 2]}`,
			want:      `{"password": "[已隐藏]", "arr": [1, 2]}`,
			forbidden: []string{"my token", "abc"},
		},

		// 4. Real combined http.Header marshaled as JSON, %#v, and %v
		{
			name:      "combined_http_header_json_marshal",
			in:        multiHdrJSON,
			want:      `{"Authorization":["Basic [已隐藏]","Basic [已隐藏]"],"Proxy-Authenticate":["Basic realm=\"ColabOne\""],"Proxy-Authorization":["","Basic [已隐藏]"],"X-Colab-Runtime-Proxy-Token":["[已隐藏]","[已隐藏]"],"api_key":["","[已隐藏]"]}`,
			forbidden: []string{"aaa", "bbb", "ccc", "tok1", "tok2", "sk-second"},
		},
		{
			name:      "combined_http_header_go_syntax",
			in:        fmt.Sprintf("%#v", multiHdr),
			want:      `http.Header{"Authorization":[]string{"Basic [已隐藏]", "Basic [已隐藏]"}, "Proxy-Authenticate":[]string{"Basic realm=\"ColabOne\""}, "Proxy-Authorization":[]string{"", "Basic [已隐藏]"}, "X-Colab-Runtime-Proxy-Token":[]string{"[已隐藏]", "[已隐藏]"}, "api_key":[]string{"", "[已隐藏]"}}`,
			forbidden: []string{"aaa", "bbb", "ccc", "tok1", "tok2", "sk-second"},
		},
		{
			name:      "combined_http_header_go_percent_v",
			in:        fmt.Sprintf("%v", multiHdr),
			want:      `map[Authorization:[Basic [已隐藏] Basic [已隐藏]] Proxy-Authenticate:[Basic realm="ColabOne"] Proxy-Authorization:[ Basic [已隐藏]] X-Colab-Runtime-Proxy-Token:[[已隐藏] [已隐藏]] api_key:[ [已隐藏]]]`,
			forbidden: []string{"aaa", "bbb", "ccc", "tok1", "tok2", "sk-second"},
		},

		// 5. Non-secret headers and empty elements preserved without over-redacting
		{
			name: "non_secret_proxy_authenticate_and_empty_containers_preserved",
			in:   `Proxy-Authenticate: Basic realm="ColabOne" {"Proxy-Authenticate":["Basic realm=\"ColabOne\""]} http.Header{"Proxy-Authenticate":[]string{"Basic realm=\"ColabOne\""}} map[Proxy-Authenticate:[Basic realm="ColabOne"]]`,
			want: `Proxy-Authenticate: Basic realm="ColabOne" {"Proxy-Authenticate":["Basic realm=\"ColabOne\""]} http.Header{"Proxy-Authenticate":[]string{"Basic realm=\"ColabOne\""}} map[Proxy-Authenticate:[Basic realm="ColabOne"]]`,
		},
		{
			name: "empty_elements_and_empty_containers_across_all_formats_preserved",
			in:   `{"Authorization":[]} {"Authorization":[""]} {"X-Colab-Runtime-Proxy-Token":[]} {"X-Colab-Runtime-Proxy-Token":["", ""]} http.Header{"Authorization":[]string{}} http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"", ""}} map[Authorization:[]] map[X-Colab-Runtime-Proxy-Token:[]]`,
			want: `{"Authorization":[]} {"Authorization":[""]} {"X-Colab-Runtime-Proxy-Token":[]} {"X-Colab-Runtime-Proxy-Token":["", ""]} http.Header{"Authorization":[]string{}} http.Header{"X-Colab-Runtime-Proxy-Token":[]string{"", ""}} map[Authorization:[]] map[X-Colab-Runtime-Proxy-Token:[]]`,
		},
		{
			name:      "go_percent_v_auth_does_not_bleed_into_adjacent_proxy_authenticate",
			in:        `map[Authorization:[Basic aaa] Proxy-Authenticate:[Basic realm="ColabOne"]]`,
			want:      `map[Authorization:[Basic [已隐藏]] Proxy-Authenticate:[Basic realm="ColabOne"]]`,
			forbidden: []string{"aaa"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Errorf("SafeMessage(%q) leaked secret %q in %q", tc.in, secret, got)
				}
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage is not idempotent for %q:\n first: %q\nsecond: %q", tc.in, got, again)
			}
		})
	}

	t.Run("adversarial_repeated_unclosed_brackets_remain_linear", func(t *testing.T) {
		for _, input := range []string{
			strings.Repeat("token:[a;", 4000) + "]",
			strings.Repeat("Authorization:[Basic a;", 4000) + "]",
			strings.Repeat("token:[a}", 4000) + "]",
		} {
			out := SafeMessage(input)
			if utf8.RuneCountInString(out) > maxMessageRunes {
				t.Fatalf("output exceeded maxMessageRunes: %d", utf8.RuneCountInString(out))
			}
		}
	})
}

func TestSafeMessageIdempotencyFrameIsolationAndStrayQuotes(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		forbidden []string
	}{
		// Fix 1: key-value idempotency, URL-valued secrets, placeholder prefixes, [-prefixed scalars, and [凭据已隐藏]
		{
			name:      "kv_unquoted_token_idempotent",
			in:        "token=secret",
			want:      "token=[已隐藏]",
			forbidden: []string{"secret"},
		},
		{
			name:      "kv_unquoted_multiple_keys_idempotent",
			in:        "refresh_token=r_secret password=p_secret api_key=sk-123 X-Colab-Runtime-Proxy-Token: proxy_secret",
			want:      "refresh_token=[已隐藏] password=[已隐藏] api_key=[已隐藏] X-Colab-Runtime-Proxy-Token: [已隐藏]",
			forbidden: []string{"r_secret", "p_secret", "sk-123", "proxy_secret"},
		},
		{
			name:      "kv_url_value_no_double_brackets",
			in:        "token=https://example.com/a",
			want:      "token=[已隐藏]",
			forbidden: []string{"example.com", "[[已隐藏]]"},
		},
		{
			name:      "kv_url_value_preserves_trailing_delimiter",
			in:        "token=https://user:pass@example.com/path?x=1, next=ok (code=wss://proxy.example.com/ws)",
			want:      "token=[已隐藏], next=ok (code=[已隐藏])",
			forbidden: []string{"example.com", "pass", "[[已隐藏]]"},
		},
		{
			name:      "kv_placeholder_prefix_with_leaked_tail",
			in:        "token=[已隐藏]leaked_secret api_key=[链接已隐藏]tail2 password=[凭据已隐藏]tail3",
			want:      "token=[已隐藏] api_key=[已隐藏] password=[已隐藏]",
			forbidden: []string{"leaked_secret", "tail2", "tail3"},
		},
		{
			name:      "kv_placeholder_prefix_with_nested_brackets_and_outer_map_close",
			in:        "token=[已隐藏][more]leaked map[token:[已隐藏]]",
			want:      "token=[已隐藏] map[token:[已隐藏]]",
			forbidden: []string{"more", "leaked"},
		},
		{
			name:      "kv_bracket_prefixed_scalar_password",
			in:        "password=[P@ss]word123",
			want:      "password=[已隐藏]",
			forbidden: []string{"P@ss", "word123"},
		},
		{
			name:      "kv_multi_bracket_prefixed_scalar_password",
			in:        "password=[a][b]c",
			want:      "password=[已隐藏]",
			forbidden: []string{"[a]", "[b]", "]c"},
		},
		{
			name:      "kv_bracket_prefixed_scalar_preserves_outer_delimiters",
			in:        "map[password:[a][b]c] password=[P@ss]word123, ok=1; token=[x]y}",
			want:      "map[password:[已隐藏]] password=[已隐藏], ok=1; token=[已隐藏]}",
			forbidden: []string{"P@ss", "word123", "[a]", "[b]"},
		},
		{
			name:      "bearer_and_basic_credential_placeholder_markers",
			in:        "Bearer [凭据已隐藏] Bearer [凭据已隐藏]leaked Authorization: Basic [凭据已隐藏] map[Authorization:[Basic [凭据已隐藏]]]",
			want:      "Bearer [已隐藏] Bearer [已隐藏] Authorization: Basic [已隐藏] map[Authorization:[Basic [已隐藏]]]",
			forbidden: []string{"leaked", "[已隐藏]] "},
		},

		// Fix 2: per-frame allowNewlines & braceDepth isolation
		{
			name:      "outer_single_line_bracket_does_not_poison_inner_multiline_secret_array",
			in:        "token:[a; \"token\":[\n  \"secret1\",\n  null,\n  \"secret2\"\n]",
			want:      "token:[[已隐藏]; \"token\":[\n  \"[已隐藏]\",\n  [已隐藏],\n  \"[已隐藏]\"\n]",
			forbidden: []string{"secret1", "secret2"},
		},
		{
			name:      "outer_single_line_bracket_does_not_poison_inner_multiline_auth_array",
			in:        "Authorization:[a; \"Authorization\":[\n  \"Basic s1\",\n  null,\n  \"Basic s2\"\n]",
			want:      "Authorization:[a; \"Authorization\":[\n  \"Basic [已隐藏]\",\n  null,\n  \"Basic [已隐藏]\"\n]",
			forbidden: []string{"Basic s1", "Basic s2"},
		},
		{
			name:      "outer_multiline_bracket_survives_inner_single_line_newline_abort",
			in:        "token:[\"s1\", [a\n  \"s2\"]",
			want:      "token:[\"[已隐藏]\", [已隐藏]\n  \"[已隐藏]\"]",
			forbidden: []string{"s1", "s2"},
		},
		{
			name:      "three_level_mixed_allow_newlines_across_newline",
			in:        "token:[\"s1\", [a; \"token\":[\n  \"s2\",\n  \"s3\"\n], \"s4\"]",
			want:      "token:[\"[已隐藏]\", [已隐藏] \"[已隐藏]\"[已隐藏]\n  \"[已隐藏]\",\n  \"[已隐藏]\"\n[已隐藏], \"[已隐藏]\"]",
			forbidden: []string{"s1", "s2", "s3", "s4"},
		},
		{
			name:      "per_frame_brace_depth_isolates_unclosed_inner_auth_from_proxy_authenticate",
			in:        `Authorization:[{; {"Authorization":[Basic a}, "Proxy-Authenticate":"Basic realm=\"ColabOne\""]`,
			want:      `Authorization:[{; {"Authorization":[Basic [已隐藏]}, "Proxy-Authenticate":"Basic realm=\"ColabOne\""]`,
			forbidden: []string{"Basic a"},
		},
		{
			name:      "per_frame_brace_depth_with_trailing_brace_and_bracket_still_preserves_proxy_authenticate",
			in:        `Authorization:[{; {"Authorization":[Basic a}, "Proxy-Authenticate":"Basic realm=\"ColabOne\""]} extra]`,
			want:      `Authorization:[{; {"Authorization":[Basic [已隐藏]}, "Proxy-Authenticate":"Basic realm=\"ColabOne\""]} extra]`,
			forbidden: []string{"Basic a"},
		},
		{
			name:      "inner_closed_bracket_inside_outer_brace_preserved",
			in:        `token:[{; {"token":[a b]}, "ok":true]`,
			want:      `token:[{; {"token":[[已隐藏] [已隐藏]]}, "ok":true]`,
			forbidden: []string{"[a", "b]"},
		},

		// Fix 3: stray unbalanced quotes inside Go %v unquoted bracket containers
		{
			name:      "go_percent_v_proxy_token_with_stray_single_quote",
			in:        fmt.Sprintf("%v", http.Header{"X-Colab-Runtime-Proxy-Token": {"tok1", "'tok2"}}),
			want:      `map[X-Colab-Runtime-Proxy-Token:[[已隐藏] '[已隐藏]]]`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "go_percent_v_proxy_token_with_stray_double_quote",
			in:        fmt.Sprintf("%v", http.Header{"X-Colab-Runtime-Proxy-Token": {"tok1", "\"tok2"}}),
			want:      `map[X-Colab-Runtime-Proxy-Token:[[已隐藏] "[已隐藏]]]`,
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "go_percent_v_proxy_token_with_stray_quote_and_third_element",
			in:        fmt.Sprintf("%v", http.Header{"X-Colab-Runtime-Proxy-Token": {"tok1", "'tok2", "tok3"}}),
			want:      `map[X-Colab-Runtime-Proxy-Token:[[已隐藏] '[已隐藏]]]`,
			forbidden: []string{"tok1", "tok2", "tok3"},
		},
		{
			name:      "go_percent_v_authorization_with_stray_double_quote",
			in:        fmt.Sprintf("%v", http.Header{"Authorization": {"Basic aaa", "\"Basic bbb"}}),
			want:      `map[Authorization:[Basic [已隐藏] "Basic [已隐藏]]]`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "go_percent_v_authorization_with_stray_single_quote",
			in:        fmt.Sprintf("%v", http.Header{"Authorization": {"Basic aaa", "'Basic bbb"}}),
			want:      `map[Authorization:[Basic [已隐藏] 'Basic [已隐藏]]]`,
			forbidden: []string{"aaa", "bbb"},
		},
		{
			name:      "go_percent_v_authorization_with_stray_quote_before_third_basic_element",
			in:        fmt.Sprintf("%v", http.Header{"Authorization": {"Basic aaa", "\"note", "Basic bbb"}}),
			want:      `map[Authorization:[Basic [已隐藏] "note Basic [已隐藏]]]`,
			forbidden: []string{"aaa", "bbb"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Errorf("SafeMessage(%q) leaked %q in %q", tc.in, secret, got)
				}
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage is not idempotent for %q:\n first: %q\nsecond: %q", tc.in, got, again)
			}
		})
	}

	t.Run("adversarial_mixed_frames_and_stray_quotes_remain_linear", func(t *testing.T) {
		for _, input := range []string{
			strings.Repeat("token:[a; \"token\":[\"x\", ", 4000) + "\n" + strings.Repeat("]", 4000),
			strings.Repeat("token:[\"a\", ", 2000) + strings.Repeat("[b\n", 2000),
			strings.Repeat("token:[a; \"token\":[\n", 4000) + "\"secret\"\n" + strings.Repeat("]", 4000),
			"token:[" + strings.Repeat(`"a\" `, 4000) + "]",
			"token:[\" " + strings.Repeat("a ", 4000) + "]",
		} {
			out := SafeMessage(input)
			if utf8.RuneCountInString(out) > maxMessageRunes {
				t.Fatalf("output exceeded maxMessageRunes: %d", utf8.RuneCountInString(out))
			}
		}
	})
}

func TestSafeMessageHardeningSixBugRegressions(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		forbidden []string
		mustKeep  []string
	}{
		// Fix 2: Propagate top.braceDepth on ']' rejection.
		{
			name:      "fix2_auth_propagates_brace_depth_on_inner_bracket_rejection",
			in:        `Authorization:["Basic s1", [ { ] } "Basic s2"]`,
			want:      `Authorization:["Basic [已隐藏]", [ { ] } "Basic [已隐藏]"]`,
			forbidden: []string{"Basic s1", "Basic s2"},
		},
		{
			name:      "fix2_token_propagates_brace_depth_on_inner_bracket_rejection",
			in:        `token:["s1", [ { ] } "s2"]`,
			want:      `token:["[已隐藏]", [已隐藏] [已隐藏] [已隐藏] [已隐藏] "[已隐藏]"]`,
			forbidden: []string{`"s1"`, `"s2"`},
		},
		{
			name:     "fix2_auth_propagated_brace_depth_rejects_outer_close_and_preserves_proxy_authenticate",
			in:       `Authorization:[ [ { ] "Proxy-Authenticate":"Basic realm=\"ColabOne\""]`,
			want:     `Authorization:[ [ { ] "Proxy-Authenticate":"Basic realm=\"ColabOne\""]`,
			mustKeep: []string{`"Proxy-Authenticate":"Basic realm=\"ColabOne\""`},
		},
		{
			name:      "fix2_multi_nested_brace_depth_propagation",
			in:        `Authorization:["Basic s1", [ { [ { ] ] } } "Basic s2"]`,
			want:      `Authorization:["Basic [已隐藏]", [ { [ { ] ] } } "Basic [已隐藏]"]`,
			forbidden: []string{"Basic s1", "Basic s2"},
		},

		// Fix 3: Unified balanced-bracket scalar scanner & zero orphaned ']'.
		{
			name:      "fix3_mid_secret_balanced_brackets_no_tail_leak",
			in:        `password=abc[def]ghi`,
			want:      `password=[已隐藏]`,
			forbidden: []string{"abc", "def", "ghi", "]ghi"},
		},
		{
			name:      "fix3_mid_secret_balanced_brackets_preserves_outer_map_close",
			in:        `map[password:abc[def]ghi]`,
			want:      `map[password:[已隐藏]]`,
			forbidden: []string{"abc", "def", "ghi"},
		},
		{
			name:      "fix3_unclosed_outer_bracket_with_balanced_scalar_tail_idempotent",
			in:        `token:[a; token=b[]c`,
			want:      `token:[[已隐藏]; token=[已隐藏]`,
			forbidden: []string{"b[]c", "]c"},
		},
		{
			name:      "fix3_unclosed_outer_bracket_with_trailing_empty_brackets_idempotent",
			in:        `token:[a; token=b[]`,
			want:      `token:[[已隐藏]; token=[已隐藏]`,
			forbidden: []string{"b[]"},
		},
		{
			name: "fix3_unclosed_double_open_bracket_semicolon_close_idempotent",
			in:   `token:[[;]`,
			want: `token:[[;]`,
		},
		{
			name: "fix3_unclosed_double_open_bracket_semicolon_close_with_tail_idempotent",
			in:   `token:[[;]x`,
			want: `token:[[;]x`,
		},
		{
			name:      "fix3_unclosed_double_open_bracket_redacts_inner_secret_idempotently",
			in:        `token:[[secret;]`,
			want:      `token:[[[已隐藏];]`,
			forbidden: []string{"secret"},
		},
		{
			name:      "fix3_unclosed_bracket_with_nested_balanced_brackets_and_secrets",
			in:        `token:[a[b]c;`,
			want:      `token:[[已隐藏][[已隐藏]][已隐藏];`,
			forbidden: []string{"[a", "[b]", "]c"},
		},

		// Fix 4: Multiline & truncated JSON arrays with null / true / false / numbers.
		{
			name:      "fix4_truncated_multiline_auth_array_with_null",
			in:        "{\"Authorization\":[\n  \"Basic s1\",\n  null,\n  \"Basic s2",
			want:      "{\"Authorization\":[\n  \"Basic [已隐藏]\",\n  null,\n  \"Basic [已隐藏]",
			forbidden: []string{"Basic s1", "Basic s2"},
		},
		{
			name:      "fix4_truncated_multiline_proxy_token_array_with_null",
			in:        "{\"X-Colab-Runtime-Proxy-Token\":[\n  \"tok1\",\n  null,\n  \"tok2",
			want:      "{\"X-Colab-Runtime-Proxy-Token\":[\n  \"[已隐藏]\",\n  [已隐藏],\n  \"[已隐藏]",
			forbidden: []string{"tok1", "tok2"},
		},
		{
			name:      "fix4_multiline_auth_array_starting_with_null",
			in:        "{\n  \"Authorization\": [\n    null,\n    \"Basic s2\"\n  ]\n}",
			want:      "{\n  \"Authorization\": [\n    null,\n    \"Basic [已隐藏]\"\n  ]\n}",
			forbidden: []string{"Basic s2"},
		},
		{
			name:      "fix4_multiline_token_array_starting_with_null",
			in:        "{\n  \"token\": [\n    null,\n    \"secret2\"\n  ]\n}",
			want:      "{\n  \"token\": [\n    [已隐藏],\n    \"[已隐藏]\"\n  ]\n}",
			forbidden: []string{"secret2"},
		},
		{
			name:      "fix4_truncated_multiline_auth_array_with_bools_and_numbers",
			in:        "{\"Authorization\":[\n  null,\n  true,\n  false,\n  -42.5e2,\n  \"Basic s3",
			want:      "{\"Authorization\":[\n  null,\n  true,\n  false,\n  -42.5e2,\n  \"Basic [已隐藏]",
			forbidden: []string{"Basic s3"},
		},
		{
			name:      "fix4_truncated_multiline_token_array_starting_with_number_redacts_numeric_and_string",
			in:        "{\"token\":[\n  123456,\n  true,\n  false,\n  \"secret3",
			want:      "{\"token\":[\n  [已隐藏],\n  [已隐藏],\n  [已隐藏],\n  \"[已隐藏]",
			forbidden: []string{"123456", "secret3"},
		},
		{
			name:      "fix4_go_percent_v_unquoted_numeric_slice_without_comma_still_redacts_all",
			in:        `map[token:[123456 secret2`,
			want:      `map[token:[[已隐藏] [已隐藏]`,
			forbidden: []string{"123456", "secret2"},
		},

		// Fix 5: '\' escape handling + Bearer/Basic balanced bracket tail.
		{
			name:      "fix5_bracket_prefixed_password_with_backslash_escape",
			in:        `password=[P@ss]\bar`,
			want:      `password=[已隐藏]`,
			forbidden: []string{"P@ss", "bar"},
		},
		{
			name:      "fix5_bracket_prefixed_password_with_char_and_backslash_escape",
			in:        `password=[P@ss]a\bar`,
			want:      `password=[已隐藏]`,
			forbidden: []string{"P@ss", "a\\bar", "bar"},
		},
		{
			name:      "fix5_placeholder_prefixed_password_with_backslash_escape",
			in:        `password=[已隐藏]\bar`,
			want:      `password=[已隐藏]`,
			forbidden: []string{"bar"},
		},
		{
			name:      "fix5_backslash_consumes_full_multibyte_utf8_rune",
			in:        "password=[P@ss]\\中bar",
			want:      "password=[已隐藏]",
			forbidden: []string{"P@ss", "中", "bar"},
		},
		{
			name:      "fix5_backslash_before_quote_still_terminates_unquoted_secret",
			in:        `password=foo\"`,
			want:      `password=[已隐藏]\"`,
			forbidden: []string{"foo"},
		},
		{
			name:      "fix5_bearer_placeholder_with_balanced_bracket_tail",
			in:        `Bearer [已隐藏][more]leaked`,
			want:      `Bearer [已隐藏]`,
			forbidden: []string{"more", "leaked"},
		},
		{
			name:      "fix5_basic_placeholder_with_balanced_bracket_tail",
			in:        `Authorization: Basic [已隐藏][more]leaked`,
			want:      `Authorization: Basic [已隐藏]`,
			forbidden: []string{"more", "leaked"},
		},
		{
			name:      "fix5_bearer_preserves_outer_slice_closing_bracket",
			in:        `Bearer [已隐藏]]`,
			want:      `Bearer [已隐藏]]`,
			mustKeep:  []string{`Bearer [已隐藏]]`},
		},
		{
			name:      "fix5_go_map_bearer_and_basic_balanced_tail_preserves_outer_brackets",
			in:        `map[Authorization:[Bearer [已隐藏][more]leaked Basic [凭据已隐藏][x]tail]]`,
			want:      `map[Authorization:[Bearer [已隐藏] Basic [已隐藏]]]`,
			forbidden: []string{"more", "leaked", "[x]", "tail"},
		},

		// Fix 6: Unclosed '[' inside a valid JSON string array must NOT escape closing quote into siblings.
		{
			name:     "fix6_unclosed_auth_bracket_in_json_string_array_preserves_sibling_proxy_authenticate",
			in:       `["Authorization:[a", "Proxy-Authenticate: Basic realm=\"ColabOne\""]`,
			want:     `["Authorization:[a", "Proxy-Authenticate: Basic realm=\"ColabOne\""]`,
			mustKeep: []string{`"Proxy-Authenticate: Basic realm=\"ColabOne\""`},
		},
		{
			name:     "fix6_unclosed_token_bracket_in_json_string_array_preserves_sibling_public_info",
			in:       `["token:[a", "public-info"]`,
			want:     `["token:[[已隐藏]", "public-info"]`,
			mustKeep: []string{`"public-info"`},
		},
		{
			name:     "fix6_empty_unclosed_token_bracket_in_json_string_array_preserves_sibling",
			in:       `["token:[", "public-info"]`,
			want:     `["token:[", "public-info"]`,
			mustKeep: []string{`"public-info"`},
		},
		{
			name:     "fix6_whitespace_unclosed_token_bracket_in_json_string_array_preserves_sibling",
			in:       `["token:[ ", "public-info"]`,
			want:     `["token:[ ", "public-info"]`,
			mustKeep: []string{`"public-info"`},
		},
		{
			name:      "fix6_unclosed_auth_basic_in_json_string_array_redacts_secret_and_preserves_sibling",
			in:        `["Authorization:[Basic s1", "Proxy-Authenticate: Basic realm=\"ColabOne\""]`,
			want:      `["Authorization:[Basic [已隐藏]", "Proxy-Authenticate: Basic realm=\"ColabOne\""]`,
			forbidden: []string{"Basic s1"},
			mustKeep:  []string{`"Proxy-Authenticate: Basic realm=\"ColabOne\""`},
		},
		{
			name:     "fix6_escaped_percent_q_json_string_array_preserves_sibling",
			in:       fmt.Sprintf("%q", `["token:[a", "public-info"]`),
			want:     `"[\"token:[[已隐藏]\", \"public-info\"]"`,
			mustKeep: []string{`\"public-info\"`},
		},
		{
			name:     "fix6_unclosed_auth_bracket_in_json_object_does_not_capture_later_array_close",
			in:       `{"msg":"Authorization:[a","Proxy-Authenticate":"Basic realm=\"ColabOne\"","arr":[1]}`,
			want:     `{"msg":"Authorization:[a","Proxy-Authenticate":"Basic realm=\"ColabOne\"","arr":[1]}`,
			mustKeep: []string{`"Proxy-Authenticate":"Basic realm=\"ColabOne\""`, `"arr":[1]`},
		},
		{
			name:      "fix6_stray_quote_before_json_auth_array_still_redacts_both_elements",
			in:        `log="unclosed; {"Authorization":["Basic aaa", "Basic bbb"]}`,
			want:      `log="unclosed; {"Authorization":["Basic [已隐藏]", "Basic [已隐藏]"]}`,
			forbidden: []string{"Basic aaa", "Basic bbb"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Errorf("SafeMessage(%q) leaked %q in %q", tc.in, secret, got)
				}
			}
			for _, keep := range tc.mustKeep {
				if !strings.Contains(got, keep) {
					t.Errorf("SafeMessage(%q) failed to preserve %q in %q", tc.in, keep, got)
				}
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage is not idempotent for %q:\n first: %q\nsecond: %q", tc.in, got, again)
			}
		})
	}
}

func TestSafeMessageLinearTimeNoQuadraticDoS(t *testing.T) {
	// First verify strict idempotency on short (non-truncated) versions of each adversarial pattern.
	for _, short := range []string{
		strings.Repeat("token:[a\" \"", 8),
		strings.Repeat("Authorization:[a\" \"", 6),
		`{"k":"` + strings.Repeat(`token:[a","k":"`, 6) + `"}`,
		strings.Repeat(`"["token:[a`, 8),
		strings.Repeat(`token:[a"y "x`, 6) + strings.Repeat("[]", 6),
		strings.Repeat(`token:[{a"y "x`, 6) + strings.Repeat("[]", 6),
		strings.Repeat(`token:[{a"y "x`, 4) + strings.Repeat("{", 4) + strings.Repeat("}", 5),
		strings.Repeat("token:[{", 6) + strings.Repeat("]", 6),
	} {
		got := SafeMessage(short)
		if again := SafeMessage(got); again != got {
			t.Fatalf("SafeMessage not idempotent on short adversarial %q:\n first: %q\nsecond: %q", short, got, again)
		}
	}

	requiredInputs := []struct {
		name string
		in   string
	}{
		{
			name: "repeated_token_unclosed_bracket_quotes_20k",
			in:   strings.Repeat("token:[a\" \"", 20000),
		},
		{
			name: "repeated_auth_unclosed_bracket_quotes_20k",
			in:   strings.Repeat("Authorization:[a\" \"", 20000),
		},
		{
			name: "repeated_json_string_token_unclosed_bracket_20k",
			in:   `{"k":"` + strings.Repeat(`token:[a","k":"`, 20000) + `"}`,
		},
		{
			name: "repeated_quoted_bracket_token_unclosed_20k",
			in:   strings.Repeat(`"["token:[a`, 20000),
		},
	}

	extraInputs := []struct {
		name string
		in   string
	}{
		{
			name: "repeated_unbounded_quote_convergence_with_trailing_brackets",
			in:   strings.Repeat(`token:[a"y "x`, 10000) + strings.Repeat("[]", 10000),
		},
		{
			name: "repeated_unbounded_brace_depth_quote_convergence",
			in:   strings.Repeat(`token:[{a"y "x`, 10000) + strings.Repeat("[]", 10000),
		},
		{
			name: "repeated_unbounded_brace_drop_chain",
			in:   strings.Repeat(`token:[{a"y "x`, 5000) + strings.Repeat("{", 5000) + strings.Repeat("}", 5001),
		},
		{
			name: "repeated_nested_braced_unbalanced_brackets",
			in:   strings.Repeat("token:[{", 10000) + strings.Repeat("]", 10000),
		},
	}

	perRequiredLimit := 200 * time.Millisecond
	totalRequiredLimit := 500 * time.Millisecond
	perExtraLimit := 250 * time.Millisecond
	if testing.CoverMode() != "" {
		// Coverage instrumentation adds counter increments inside hot byte-scanning
		// loops, inflating wall-clock runtime while keeping O(N) scaling intact.
		perRequiredLimit = 1500 * time.Millisecond
		totalRequiredLimit = 4 * time.Second
		perExtraLimit = 1500 * time.Millisecond
	}

	startRequired := time.Now()
	for _, tc := range requiredInputs {
		start := time.Now()
		got := SafeMessage(tc.in)
		elapsed := time.Since(start)
		t.Logf("%s: %v", tc.name, elapsed)
		if utf8.RuneCountInString(got) > maxMessageRunes {
			t.Fatalf("%s: output exceeded maxMessageRunes: %d", tc.name, utf8.RuneCountInString(got))
		}
		if elapsed > perRequiredLimit {
			t.Errorf("%s: took %v (> %v), expected strictly linear O(N) performance", tc.name, elapsed, perRequiredLimit)
		}
	}
	if totalReq := time.Since(startRequired); totalReq > totalRequiredLimit {
		t.Errorf("total elapsed for the four 20,000-rep inputs was %v (> %v)", totalReq, totalRequiredLimit)
	}

	for _, tc := range extraInputs {
		start := time.Now()
		got := SafeMessage(tc.in)
		elapsed := time.Since(start)
		t.Logf("%s: %v", tc.name, elapsed)
		if utf8.RuneCountInString(got) > maxMessageRunes {
			t.Fatalf("%s: output exceeded maxMessageRunes: %d", tc.name, utf8.RuneCountInString(got))
		}
		if elapsed > perExtraLimit {
			t.Errorf("%s: took %v (> %v), expected strictly linear O(N) performance", tc.name, elapsed, perExtraLimit)
		}
	}
}

// naiveMatchCloseOracle is a simple uncached reference implementation of the
// bracket matching semantics used to differentially verify bracketMatcher's
// memoization across complex nested combinations.
func naiveMatchCloseOracle(text string, openIdx int) int {
	if openIdx < 0 || openIdx >= len(text) || text[openIdx] != '[' {
		return -1
	}
	stack := []bracketFrame{{
		openIdx:       openIdx,
		allowNewlines: containerAllowsNewlines(text, openIdx),
	}}
	atElemStart := true
	i := openIdx + 1
	for i < len(text) {
		b := text[i]
		if b == '\r' || b == '\n' {
			if !stack[0].allowNewlines {
				return -1
			}
			var kept []bracketFrame
			for _, frame := range stack {
				if !frame.allowNewlines {
					if len(kept) > 0 {
						kept[len(kept)-1].braceDepth += frame.braceDepth
					}
					continue
				}
				kept = append(kept, frame)
			}
			stack = kept
			atElemStart = true
			i++
			continue
		}
		if atElemStart && isQuotedStart(text, i) {
			_, _, closeDelim, nextPos, ok := scanQuotedString(text, i)
			if ok {
				if closeDelim == "" {
					if !stack[len(stack)-1].allowNewlines {
						atElemStart = false
						i++
						continue
					}
					return -1
				}
				i = nextPos
				atElemStart = false
				continue
			}
		}
		switch b {
		case '[':
			stack = append(stack, bracketFrame{
				openIdx:       i,
				allowNewlines: containerAllowsNewlines(text, i),
			})
			atElemStart = true
			i++
		case ']':
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.braceDepth > 0 {
				if len(stack) == 0 {
					return -1
				}
				stack[len(stack)-1].braceDepth += top.braceDepth
				atElemStart = false
				i++
				continue
			}
			if len(stack) == 0 {
				return i
			}
			atElemStart = false
			i++
		case '{':
			stack[len(stack)-1].braceDepth++
			atElemStart = true
			i++
		case '}':
			for len(stack) > 0 && stack[len(stack)-1].braceDepth == 0 {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				return -1
			}
			stack[len(stack)-1].braceDepth--
			atElemStart = false
			i++
		case ',', ':':
			atElemStart = true
			i++
		default:
			if b < utf8.RuneSelf {
				atElemStart = isASCIIUnicodeSpace(b)
				i++
			} else {
				r, size := utf8.DecodeRuneInString(text[i:])
				atElemStart = isUnicodeSpaceRune(r)
				i += size
			}
		}
	}
	return -1
}

func TestBracketMatcherMatchesUncachedOracle(t *testing.T) {
	corpus := []string{
		`["Basic s1", [ { ] } "Basic s2"]`,
		`[ [ { ] "Proxy-Authenticate":"Basic realm=\"ColabOne\""]`,
		`["s1", [a; ["s2",\n"s3"\n], "s4"]`,
		"[\"s1\", [a { [\"s2\", {\n  \"s3\"\n] } } \"s4\"]",
		"[\"s1\", [a { [b { [\"c\",\n  \"d\"\n] } } ] ]",
		"[\"s1\", [a { [\"c\",\n  \"d\" } ] } ]",
		"[a\"y \"x[a\"y \"x[a\"y \"x[]]",
		"[{a\"y \"x[{a\"y \"x{{}}]",
		"\"[\"[a\"[\"[a\"[\"[a]",
		"[null, [true, [false, [-1.5e2, \"x\"\n]\n]\n]\n]",
		"[a { [b { [c { ] ] ] } } } ]",
		"[ { [ { [ } ] } ]",
		"[\"a\", [b\n[\"c\", [d\n\"e\"]]]",
		"[ \"unclosed [a] ]",
		"[\"a\", \"unclosed [b] ]",
		"[a; [\"x\",\n[b { ] }\n\"y\"]]",
	}

	// Deterministic pseudo-random generator over tricky bracket/brace/quote/newline tokens.
	tokens := []string{"[", "]", "{", "}", "\n", `"a"`, `"`, `'`, " ", ",", "a", "null,", "[a", `["x",`}
	seed := uint64(0x9e3779b97f4a7c15)
	nextRand := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	for i := 0; i < 400; i++ {
		var sb strings.Builder
		length := 6 + nextRand(18)
		for j := 0; j < length; j++ {
			sb.WriteString(tokens[nextRand(len(tokens))])
		}
		corpus = append(corpus, sb.String())
	}

	for idx, text := range corpus {
		var opens []int
		for i := 0; i < len(text); i++ {
			if text[i] == '[' {
				opens = append(opens, i)
			}
		}

		// Forward query order (primary production access pattern).
		fwd := &bracketMatcher{text: text}
		for _, openIdx := range opens {
			got := fwd.matchClose(openIdx)
			want := naiveMatchCloseOracle(text, openIdx)
			if got != want {
				t.Fatalf("case %d (forward) openIdx=%d in %q: got %d, want %d", idx, openIdx, text, got, want)
			}
		}

		// Reverse query order (exercises nested cached outcomes from inside out).
		rev := &bracketMatcher{text: text}
		for k := len(opens) - 1; k >= 0; k-- {
			openIdx := opens[k]
			got := rev.matchClose(openIdx)
			want := naiveMatchCloseOracle(text, openIdx)
			if got != want {
				t.Fatalf("case %d (reverse) openIdx=%d in %q: got %d, want %d", idx, openIdx, text, got, want)
			}
		}
	}
}

func TestSafeMessageCoWebTokensNewSecretKeysJSONObjectsAndUserinfoDelims(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		forbidden []string
	}{
		{
			name:      "co_web_token_redacted_at_20_chars_and_above",
			in:        "cookie co_web_1234567890abcdefghij and co_web_1234567890abcdefghijklmnop_qrs",
			want:      "cookie [凭据已隐藏] and [凭据已隐藏]",
			forbidden: []string{"co_web_1234567890abcdefghij", "abcdefghijklmnop_qrs"},
		},
		{
			name: "co_web_token_below_min_length_untouched",
			in:   "short co_web_1234567890abcdefghi ok",
			want: "short co_web_1234567890abcdefghi ok",
		},
		{
			name:      "new_secret_key_suffixes_json_and_assignments",
			in:        `{"stream_key":"live-sk-1","secret":"top-s2","api-key":"ak-3","client-secret":"cs-4","secret_key":"sk-5","secret-key":"sk-6"} stream_key=v1 secret=v2 api-key=v3 client-secret=v4 secret_key=v5 secret-key=v6`,
			want:      `{"stream_key":"[已隐藏]","secret":"[已隐藏]","api-key":"[已隐藏]","client-secret":"[已隐藏]","secret_key":"[已隐藏]","secret-key":"[已隐藏]"} stream_key=[已隐藏] secret=[已隐藏] api-key=[已隐藏] client-secret=[已隐藏] secret_key=[已隐藏] secret-key=[已隐藏]`,
			forbidden: []string{"live-sk-1", "top-s2", "ak-3", "cs-4", "sk-5", "sk-6", "=v1", "=v2", "=v3", "=v4", "=v5", "=v6"},
		},
		{
			name:      "json_object_bound_to_secret_key_redacts_scalar_strings",
			in:        `{"token":{"access":"acc-123","refresh":"ref-456","expires_in":3600,"empty":"","nested":{"k":"deep-789"},"items":["arr-1",""]},"ok":true}`,
			want:      `{"token":{"access":"[已隐藏]","refresh":"[已隐藏]","expires_in":3600,"empty":"","nested":{"k":"[已隐藏]"},"items":["[已隐藏]",""]},"ok":true}`,
			forbidden: []string{"acc-123", "ref-456", "deep-789", "arr-1"},
		},
		{
			name:      "escaped_json_object_bound_to_secret_key",
			in:        fmt.Sprintf("%q", `{"secret":{"a":"val1","b":"val2"}}`),
			want:      `"{\"secret\":{\"a\":\"[已隐藏]\",\"b\":\"[已隐藏]\"}}"`,
			forbidden: []string{"val1", "val2"},
		},
		{
			name: "empty_json_object_bound_to_secret_key_preserved",
			in:   `{"token":{},"ok":true}`,
			want: `{"token":{},"ok":true}`,
		},
		{
			name:      "truncated_unclosed_json_object_bound_to_secret_key",
			in:        `{"token":{"a":"secret1","b":"secret2`,
			want:      `{"token":{"a":"[已隐藏]","b":"[已隐藏]`,
			forbidden: []string{"secret1", "secret2"},
		},
		{
			name:      "truncated_unclosed_json_object_with_unquoted_object_and_array_secrets",
			in:        `{"token":{"a":unquotedObjSecret,"items":[unquotedArr1, "quotedArr2`,
			want:      `{"token":{"a":[已隐藏],"items":[[已隐藏], "[已隐藏]`,
			forbidden: []string{"unquotedObjSecret", "unquotedArr1", "quotedArr2"},
		},
		{
			name:      "closed_and_unclosed_json_object_unquoted_secrets_idempotent",
			in:        `{"secret":{"a":bareSecretA,"b":[bareSecretB]}} {"token":{"x":bareSecretX`,
			want:      `{"secret":{"a":[已隐藏],"b":[[已隐藏]]}} {"token":{"x":[已隐藏]`,
			forbidden: []string{"bareSecretA", "bareSecretB", "bareSecretX"},
		},
		{
			name:      "url_userinfo_with_unescaped_close_paren_and_bracket",
			in:        `socks5://alice:p)ss@10.0.0.2:1080 socks5h://bob:p]ss@10.0.0.3:1080/x https://carol:p[a]b)c@[2001:db8::1]:8443/api?k=1`,
			want:      `[链接已隐藏] [链接已隐藏] [链接已隐藏]`,
			forbidden: []string{"p)ss", "p]ss", "10.0.0.2", "10.0.0.3", "2001:db8::1"},
		},
		{
			name:      "bracketed_and_parenthesized_urls_with_userinfo_delimiters_preserve_outer_wrappers",
			in:        `see (socks5://alice:p)ss@10.0.0.2:1080). and [socks5://bob:p]ss@10.0.0.3:1080]; and [https://[2001:db8::1]]; and (https://example.com/a).`,
			want:      `see ([链接已隐藏]). and [[链接已隐藏]]; and [[链接已隐藏]]; and ([链接已隐藏]).`,
			forbidden: []string{"p)ss", "p]ss", "10.0.0.2", "10.0.0.3", "2001:db8::1", "example.com"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeMessage(tc.in)
			if got != tc.want {
				t.Errorf("SafeMessage(%q):\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Errorf("SafeMessage(%q) leaked %q in %q", tc.in, secret, got)
				}
			}
			if again := SafeMessage(got); again != got {
				t.Errorf("SafeMessage is not idempotent for %q:\n first: %q\nsecond: %q", tc.in, got, again)
			}
		})
	}

	t.Run("adversarial_repeated_unclosed_secret_braces_remain_linear", func(t *testing.T) {
		input := strings.Repeat(`token:{"a":`, 20000)
		start := time.Now()
		got := SafeMessage(input)
		elapsed := time.Since(start)
		if utf8.RuneCountInString(got) > maxMessageRunes {
			t.Fatalf("output exceeded maxMessageRunes: %d", utf8.RuneCountInString(got))
		}
		limit := 300 * time.Millisecond
		if testing.CoverMode() != "" {
			limit = 2 * time.Second
		}
		if elapsed > limit {
			t.Fatalf("repeated unclosed secret braces took %v (> %v), expected O(N)", elapsed, limit)
		}
	})
}

func TestActivityRedactsGoogleClientSecretAndHardensDiskLogSymlink(t *testing.T) {
	got := SafeMessage("oauth failed with GOCSPX-SuperSecretClient12345678 in body")
	if strings.Contains(got, "SuperSecretClient") || !strings.Contains(got, "[凭据已隐藏]") {
		t.Fatalf("expected GOCSPX- secret redacted, got %q", got)
	}

	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	subDir := filepath.Join(dir, "private-logs")
	logPath := filepath.Join(subDir, "activity.log")
	al := NewColabOneActivityLog(10, logPath, 16*1024)
	al.Add("hello", "TEST", "info")
	al.Close()

	dirInfo, err := os.Stat(subDir)
	if err != nil {
		t.Fatalf("stat log dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("expected log dir mode 0700, got %#o", perm)
	}

	// Symlink log file must NOT be written through.
	target := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	symLog := filepath.Join(dir, "sym.log")
	if err := os.Symlink(target, symLog); err != nil {
		t.Fatal(err)
	}
	al2 := NewColabOneActivityLog(10, symLog, 16*1024)
	al2.Add("should not overwrite symlink target", "TEST", "info")
	al2.Close()
	gotTarget, err := os.ReadFile(target)
	if err != nil || string(gotTarget) != "untouched" {
		t.Fatalf("symlink log target was modified: %q err=%v", string(gotTarget), err)
	}

	// Dangling symlink log file must NOT create target file via O_CREATE.
	danglingTarget := filepath.Join(t.TempDir(), "dangling-victim.log")
	danglingLink := filepath.Join(dir, "dangling.log")
	if err := os.Symlink(danglingTarget, danglingLink); err != nil {
		t.Fatal(err)
	}
	al3 := NewColabOneActivityLog(10, danglingLink, 16*1024)
	al3.Add("must not create target via dangling symlink", "TEST", "info")
	al3.Close()
	if _, err := os.Stat(danglingTarget); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink target should not exist, stat err=%v", err)
	}
}

func TestSafeMessageRedactsShareTokens(t *testing.T) {
	galTok := "co_gal_abcdefghijkl.0123456789abcdef.fedcba9876543210"
	wfTok := "co_wf_abcdefghijkl.0123456789abcdef.fedcba9876543210"
	got := SafeMessage("failed opening " + galTok + " and " + wfTok)
	if strings.Contains(got, "co_gal_") || strings.Contains(got, "co_wf_") || strings.Contains(got, "abcdefghijkl") {
		t.Fatalf("share tokens leaked in SafeMessage: %q", got)
	}
}
