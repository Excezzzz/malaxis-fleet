package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/domain"

	"github.com/gorilla/mux"
)

// providerDomainFromRequest validates and normalizes a subscription provider domain. Only bare hostnames are accepted: no scheme, no path/query, no user info, no port (subscription URLs may use any port, but a provider mapping is per-host). Returns the lowercased domain or "" when invalid.
func providerDomainFromRequest(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	// Reject anything with a scheme / path / query / fragment / userinfo.
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#@") {
		return ""
	}
	// Provider mappings are keyed by subscription URL host: IP literals are meaningless for grouping and rejected.
	if net.ParseIP(raw) != nil {
		return ""
	}
	// Hostname sanity: letters, digits, dots and hyphens only.
	for _, c := range raw {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-') {
			return ""
		}
	}
	return raw
}

// GetProvidersHandler lists all subscription providers. Read access requires can_view_nodes so any node viewer sees the friendly names in grouped UIs.
func (a *API) GetProvidersHandler(w http.ResponseWriter, r *http.Request) {
	providers, err := a.repo.GetSubscriptionProviders()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if providers == nil {
		providers = []domain.SubscriptionProvider{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(providers)
}

// UpsertProviderHandler creates or renames a subscription provider mapping (POST with a body, PUT with the domain in the URL).
func (a *API) UpsertProviderHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageProviders) {
		return
	}

	var req struct {
		Domain string `json:"domain"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	domainKey := providerDomainFromRequest(req.Domain)
	// PUT carries the domain in the URL instead.
	if vars := mux.Vars(r); vars["domain"] != "" {
		domainKey = providerDomainFromRequest(vars["domain"])
	}
	if domainKey == "" {
		http.Error(w, "Bad Request: domain must be a valid hostname", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "Bad Request: name is required", http.StatusBadRequest)
		return
	}
	if len(name) > 64 {
		http.Error(w, "Bad Request: name must be 64 characters or fewer", http.StatusBadRequest)
		return
	}

	provider := domain.SubscriptionProvider{Domain: domainKey, Name: name}
	if err := a.repo.UpsertSubscriptionProvider(provider); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, provider.Domain, "Upserted subscription provider "+provider.Domain+" -> "+provider.Name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(provider)
}

// DeleteProviderHandler removes a subscription provider mapping.
func (a *API) DeleteProviderHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageProviders) {
		return
	}

	domainKey := providerDomainFromRequest(mux.Vars(r)["domain"])
	if domainKey == "" {
		http.Error(w, "Bad Request: domain must be a valid hostname", http.StatusBadRequest)
		return
	}

	if err := a.repo.DeleteSubscriptionProvider(domainKey); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, domainKey, "Deleted subscription provider "+domainKey)

	w.WriteHeader(http.StatusOK)
}
