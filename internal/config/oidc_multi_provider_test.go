package config

import (
	"testing"
)

// TestNormalizeOIDCProviders_EmptyKeepsSingleProviderBehaviour
// 「生产用户无感」的技术保证：没配多 IdP 时，NormalizeOIDCProviders 必须
// 返回 nil —— 调用方据此走原来的单 provider 路径，一行代码都不变。
func TestNormalizeOIDCProviders_EmptyKeepsSingleProviderBehaviour(t *testing.T) {
	cfg := &OIDCAuthConfig{
		Enable:              true,
		IssuerURL:           "http://casdoor.example.com:3012",
		ClientID:            "legacy-client-id",
		ProviderDisplayName: "组织账号",
	}
	if got := cfg.NormalizeOIDCProviders(); got != nil {
		t.Fatalf("expected nil for single-provider config, got %d entries", len(got))
	}
}

// TestNormalizeOIDCProviders_InheritsSingleProviderFields
// 多 IdP 里某项没写 issuer/client → 继承单 provider 字段。
// 这样「加第二个 IdP」只需要写它自己的那几行，不用复制整套。
func TestNormalizeOIDCProviders_InheritsSingleProviderFields(t *testing.T) {
	cfg := &OIDCAuthConfig{
		IssuerURL:           "http://default.example.com:3012",
		ClientID:            "default-client-id",
		ClientSecret:        "default-secret",
		ProviderDisplayName: "默认",
		Scopes:              []string{"openid", "email"},
		Providers: []OIDCProviderConfig{
			{ID: "zkju", DisplayName: "中科矩", IssuerURL: "http://zkju.example.com:3012"},
		},
	}
	got := cfg.NormalizeOIDCProviders()
	if len(got) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(got))
	}
	p := got[0]
	if p.ClientID != "default-client-id" {
		t.Errorf("client_id should inherit, got %q", p.ClientID)
	}
	if p.ClientSecret != "default-secret" {
		t.Errorf("client_secret should inherit, got %q", p.ClientSecret)
	}
	if p.IssuerURL != "http://zkju.example.com:3012" {
		t.Errorf("issuer_url should stay overridden, got %q", p.IssuerURL)
	}
	if len(p.Scopes) != 2 {
		t.Errorf("scopes should inherit, got %v", p.Scopes)
	}
	if p.ID != "zkju" {
		t.Errorf("id should stay, got %q", p.ID)
	}
}

// TestNormalizeOIDCProviders_GeneratesStableID
// 没写 id → 从 display_name 生成稳定 id（登录按钮的 key）。
func TestNormalizeOIDCProviders_GeneratesStableID(t *testing.T) {
	cfg := &OIDCAuthConfig{
		Providers: []OIDCProviderConfig{
			{DisplayName: "中科矩账号"},
			{DisplayName: "教育账号"},
		},
	}
	got := cfg.NormalizeOIDCProviders()
	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(got))
	}
	if got[0].ID == "" || got[1].ID == "" {
		t.Fatalf("ids must not be empty: %q %q", got[0].ID, got[1].ID)
	}
	if got[0].ID == got[1].ID {
		t.Fatalf("ids must differ, both %q", got[0].ID)
	}
	// 同样输入 → 同样输出（稳定性：前端按钮的 key 依赖它）
	again := cfg.NormalizeOIDCProviders()
	if again[0].ID != got[0].ID || again[1].ID != got[1].ID {
		t.Fatalf("ids not stable: %v vs %v", got, again)
	}
}

// TestNormalizeOIDCProviders_DeduplicatesIDs
// id 冲突 → 自动加序号，保证 EffectiveID 唯一（它是身份锚点，
// 重复会导致两个 IdP 被当成同一个）。
func TestNormalizeOIDCProviders_DeduplicatesIDs(t *testing.T) {
	cfg := &OIDCAuthConfig{
		Providers: []OIDCProviderConfig{
			{ID: "dup", DisplayName: "A"},
			{ID: "dup", DisplayName: "B"},
		},
	}
	got := cfg.NormalizeOIDCProviders()
	if got[0].ID == got[1].ID {
		t.Fatalf("duplicate ids must be disambiguated, both %q", got[0].ID)
	}
}

// TestNormalizeOIDCProviders_OwnIdpDoesNotInheritEndpoints
// provider 指向**另一个 Casdoor**（给了自己的 issuer_url）时，
// 绝不能继承单 provider 的 AuthorizationEndpoint/TokenEndpoint/JwksURI ——
// 那是上一个 Casdoor 的。
//
// 踩过的坑（2026-11-09）：edu provider 给了 434 的 issuer_url，却继承了
// 457 的 AuthorizationEndpoint → oidcNeedsDiscovery 判定「端点已全」→
// 跳过 discovery → 授权请求发到了 457（错的 Casdoor，错的 client_id 域）。
//
// 正确行为：端点留空 → 后面 populateOIDCEndpoints 走 discovery 自动填
// （discovery_url 跟着自己的 issuer 走）。
func TestNormalizeOIDCProviders_OwnIdpDoesNotInheritEndpoints(t *testing.T) {
	cfg := &OIDCAuthConfig{
		IssuerURL:             "http://casdoor-a:3012",
		DiscoveryURL:          "http://casdoor-a:3012/.well-known/openid-configuration",
		AuthorizationEndpoint: "http://casdoor-a:3012/login/oauth/authorize",
		TokenEndpoint:         "http://casdoor-a:3012/api/login/oauth/access_token",
		UserInfoEndpoint:      "http://casdoor-a:3012/api/userinfo",
		JwksURI:               "http://casdoor-a:3012/.well-known/jwks",
		ClientID:              "client-a",
		ClientSecret:          "secret-a",
		Providers: []OIDCProviderConfig{
			{ID: "zkju", IssuerURL: "http://casdoor-a:3012", ClientID: "client-a"},
			{ID: "edu", IssuerURL: "http://casdoor-b:3012", ClientID: "client-b"},
		},
	}
	got := cfg.NormalizeOIDCProviders()
	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(got))
	}
	edu := got[1]
	if edu.AuthorizationEndpoint != "" {
		t.Errorf("edu must NOT inherit another IdP's authorization endpoint, got %q", edu.AuthorizationEndpoint)
	}
	if edu.TokenEndpoint != "" {
		t.Errorf("edu must NOT inherit another IdP's token endpoint, got %q", edu.TokenEndpoint)
	}
	if edu.JwksURI != "" {
		t.Errorf("edu must NOT inherit another IdP's jwks uri, got %q", edu.JwksURI)
	}
	if edu.UserInfoEndpoint != "" {
		t.Errorf("edu must NOT inherit another IdP's userinfo endpoint, got %q", edu.UserInfoEndpoint)
	}
	// discovery_url 要跟着自己的 issuer 走
	want := "http://casdoor-b:3012/.well-known/openid-configuration"
	if edu.DiscoveryURL != want {
		t.Errorf("discovery_url should follow own issuer: want %q got %q", want, edu.DiscoveryURL)
	}
	// client_id / client_secret 仍可继承（同一客户的应用登记）
	if edu.ClientID != "client-b" {
		t.Errorf("client_id should stay, got %q", edu.ClientID)
	}
}

// TestOIDCProvidersFromEnv_JSON
// OIDC_AUTH_PROVIDERS_JSON 注入多 IdP。
func TestOIDCProvidersFromEnv_JSON(t *testing.T) {
	raw := `[{"id":"zkju","display_name":"中科矩","issuer_url":"http://a:3012","client_id":"cid-a"},
	         {"id":"edu","display_name":"教育","issuer_url":"http://b:3012","client_id":"cid-b"}]`
	t.Setenv("OIDC_AUTH_PROVIDERS_JSON", raw)

	// 复用配置加载里的注入逻辑：直接调用同一个解析路径。
	providers := parseOIDCProvidersJSON(raw)
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
	if providers[0].ID != "zkju" || providers[1].ID != "edu" {
		t.Fatalf("unexpected ids: %+v", providers)
	}
}

// TestOIDCProvidersFromEnv_BadJSONIgnored
// 配置坏了不能让整个服务起不来（OIDC 只是登录方式之一）—— 忽略并回落。
func TestOIDCProvidersFromEnv_BadJSONIgnored(t *testing.T) {
	t.Setenv("OIDC_AUTH_PROVIDERS_JSON", "{not json")
	providers := parseOIDCProvidersJSON("{not json")
	if len(providers) != 0 {
		t.Fatalf("bad json must yield 0 providers, got %d", len(providers))
	}
}
