package router

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// TestKBMaintenanceRoutesAreRegistered pins the three maintenance
// endpoints. A missing route means the feature silently does nothing — the
// failure mode is a 404 that looks like "no permission" to the caller.
func TestKBMaintenanceRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	engine := gin.New()
	v1 := engine.Group("/api/v1")

	RegisterKBMaintenanceRoutes(v1, &handler.KBMaintenanceHandler{}, g)

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/knowledge-bases/:id/maintenance/plan"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/maintenance/run"},
		{http.MethodGet, "/api/v1/knowledge-bases/:id/maintenance/runs"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if !registered[tc.method+" "+tc.path] {
				t.Fatalf("route %s %s is not registered", tc.method, tc.path)
			}
		})
	}
}

// TestKBMaintenanceRoutesRequireFullAccess pins the API-key posture: the
// maintenance endpoints rewrite the knowledge base's search index, so a
// scoped key without full access must not reach them.
func TestKBMaintenanceRoutesRequireFullAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterKBMaintenanceRoutes(v1, &handler.KBMaintenanceHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/knowledge-bases/:id/maintenance/plan"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/maintenance/run"},
		{http.MethodGet, "/api/v1/knowledge-bases/:id/maintenance/runs"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
		})
	}
}

// TestKBMaintenanceRunRouteNeedsWriteCapability pins that the write route
// (/run) is gated behind a write-capable key (manage_kbs), not behind the
// read-only retrieve capability.
//
// Why: API-key principals are authorized *solely* by the APIKeyGate policy
// (the JWT role ladder does not apply to a machine principal — see
// middleware.RequireRole). So the policy IS the only thing standing between
// a read-only integration key and a backfill that writes embedding rows and
// burns the tenant's embedding quota. /run must therefore be unreachable
// with retrieve alone, while /plan (a pure read) may keep it.
func TestKBMaintenanceRunRouteNeedsWriteCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterKBMaintenanceRoutes(v1, &handler.KBMaintenanceHandler{}, g)

	run := mustLookupAPIKeyPolicy(t, g, http.MethodPost,
		"/api/v1/knowledge-bases/:id/maintenance/run")
	if !policyHasCapability(run, types.APIKeyCapabilityManageKnowledgeBases) {
		t.Fatalf("run policy capabilities = %v, want %s",
			run.Capabilities, types.APIKeyCapabilityManageKnowledgeBases)
	}
	if policyHasCapability(run, types.APIKeyCapabilityRetrieve) {
		t.Fatalf("run policy must not accept the read-only %s capability: %v",
			types.APIKeyCapabilityRetrieve, run.Capabilities)
	}

	plan := mustLookupAPIKeyPolicy(t, g, http.MethodPost,
		"/api/v1/knowledge-bases/:id/maintenance/plan")
	if !policyHasCapability(plan, types.APIKeyCapabilityRetrieve) {
		t.Fatalf("plan policy capabilities = %v, want %s",
			plan.Capabilities, types.APIKeyCapabilityRetrieve)
	}
}
