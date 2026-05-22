package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/eschmechel/hermes-apprentice/proxy/internal/tenant"
)

type ctxKeyTenantType struct{}

var ctxKeyTenant = ctxKeyTenantType{}

func contextWithTenant(ctx context.Context, t string) context.Context {
	return context.WithValue(ctx, ctxKeyTenant, t)
}

func TenantFromContext(ctx context.Context) string {
	v := ctx.Value(ctxKeyTenant)
	if v == nil {
		return tenant.GlobalTenant
	}
	s, _ := v.(string)
	return s
}

type authHandler struct {
	tenantStore *tenant.Store
	logger      *slog.Logger
}

func newAuthHandler(ts *tenant.Store, logger *slog.Logger) *authHandler {
	return &authHandler{tenantStore: ts, logger: logger}
}

func (a *authHandler) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Apprentice-Tenant")
		apiKey := r.Header.Get("X-Apprentice-Key")
		if tenantID == "" {
			tenantID = tenant.GlobalTenant
		}

		resolved, ok := a.tenantStore.Authenticate(tenantID, apiKey)
		if !ok {
			a.logger.Warn("auth denied", "tenant", tenantID, "path", r.URL.Path)
			writeError(w, http.StatusUnauthorized, "invalid tenant or API key")
			return
		}

		r = r.WithContext(contextWithTenant(r.Context(), resolved))
		next.ServeHTTP(w, r)
	})
}
