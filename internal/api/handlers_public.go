package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/domain"

	"github.com/gorilla/mux"
)

// --- Public Handlers ---

// stripUTF8BOM removes a leading UTF-8 byte order mark (EF BB BF) from a payload. Windows PowerShell's Invoke-Expression chokes on the BOM bytes ("ï»¿#"), so served scripts must never start with it.
func stripUTF8BOM(b []byte) []byte {
	if len(b) >= 3 && bytes.Equal(b[:3], []byte{0xEF, 0xBB, 0xBF}) {
		return b[3:]
	}
	return b
}

// stripCRLF removes every carriage-return byte from a payload. Shell scripts edited on Windows are saved with CRLF line endings, which makes Bash on Linux clients misparse lines such as `set -o pipefail\r` (": invalid option namepipefail", "curl: (23) Failed writing body"). The master server always serves .sh/.bash files with clean Unix LF endings no matter how the developer saved them.
func stripCRLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r"), []byte(""))
}

// isShellScript reports whether a served file name is a POSIX shell script that must be served with Unix LF line endings.
func isShellScript(filename string) bool {
	return strings.HasSuffix(filename, ".sh") || strings.HasSuffix(filename, ".bash")
}

// HealthHandler answers with a deliberately generic probe-friendly response. No application identity, database status, or bot state is disclosed.
func (a *API) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// serveDockerCompose serves the docker-compose.yml template with the active SECRET_TOKEN injected.
func (a *API) serveDockerCompose(w http.ResponseWriter, r *http.Request) {
	fileBytes, err := deployFS.ReadFile("deploy/client-docker-compose.yml")
	if err != nil {
		http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
		log.Printf("Error reading embedded docker-compose file: %v", err)
		return
	}

	// Get the active fleet secret from config (auto-generated or user-set)
	secret := a.config.FleetSecret
	if secret == "" {
		// Fallback: try to read from database
		dbSecret, err := a.repo.GetSetting("fleet_secret")
		if err != nil {
			log.Printf("ERROR: failed to read fleet_secret from DB for docker-compose: %v", err)
		}
		if dbSecret != "" {
			secret = dbSecret
		} else {
			secret = "changeme_fleet_secret"
		}
	}

	// Replace the SECRET_TOKEN placeholder with the actual active secret
	content := string(fileBytes)
	content = strings.ReplaceAll(content, "${FLEET_SECRET:-changeme_fleet_secret}", secret)
	content = strings.ReplaceAll(content, "FLEET_SECRET", "FLEET_SECRET") // keep env var name
	content = a.applyDomainPlaceholders(content)

	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Write([]byte(content))
}

// serveAgentPackage builds a zip archive of the modular agent package (deploy/agent_src) on the fly and serves it. Used by the installers and the agent's own OTA update_client_files flow.
func (a *API) serveAgentPackage(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// go:embed silently skips files whose names start with "_", so the Python package marker __init__.py is never part of the embedded FS. It must be injected explicitly or the extracted agent_src/ would not be a package.
	fh := &zip.FileHeader{Name: "agent_src/__init__.py", Method: zip.Deflate}
	fh.SetModTime(time.Now())
	if f, err := zw.CreateHeader(fh); err == nil {
		f.Write([]byte("# -*- coding: utf-8 -*-\n\"\"\"Malaxis Fleet - modular node agent package.\"\"\"\n__version__ = \"1.1.0\"\n"))
	} else {
		log.Printf("ERROR: failed to write agent_src/__init__.py into zip: %v", err)
	}

	entries, err := deployFS.ReadDir("deploy/agent_src")
	if err != nil {
		http.Error(w, "Internal Server Error: agent package not found", http.StatusInternalServerError)
		log.Printf("Error listing deploy/agent_src: %v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := deployFS.ReadFile("deploy/agent_src/" + e.Name())
		if err != nil {
			log.Printf("Error reading deploy/agent_src/%s: %v", e.Name(), err)
			continue
		}
		header := &zip.FileHeader{Name: "agent_src/" + e.Name(), Method: zip.Deflate}
		header.SetModTime(time.Now())
		f, err := zw.CreateHeader(header)
		if err != nil {
			log.Printf("ERROR: failed to create zip entry agent_src/%s: %v", e.Name(), err)
			continue
		}
		if _, err := f.Write(data); err != nil {
			log.Printf("ERROR: failed to write zip entry agent_src/%s: %v", e.Name(), err)
			continue
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("ERROR: failed to finalize agent_src.zip: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	log.Printf("Served agent_src.zip (%d bytes, %d files)", buf.Len(), len(entries)+1)

	w.Header().Set("Content-Type", "application/zip")
	w.Write(buf.Bytes())
}

// listTemplateFiles dynamically scans the embedded deploy directory and returns every regular file within it (top level only). Any file added to internal/api/deploy automatically appears in the Web IDE.
func listTemplateFiles() []string {
	entries, err := deployFS.ReadDir("deploy")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if templateNameValid(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// templateNameValid rejects path traversal: only a flat base name (no path separators, no "..") is ever accepted.
func templateNameValid(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/\\") && !strings.Contains(name, "..")
}

// isKnownTemplate reports whether name is a valid editable client template: it must exist in the embedded deploy dir OR have a "template:" override in the DB settings table.
func (a *API) isKnownTemplate(name string) bool {
	if !templateNameValid(name) {
		return false
	}
	for _, n := range listTemplateFiles() {
		if n == name {
			return true
		}
	}
	if keys, err := a.repo.GetSettingKeysByPrefix("template:"); err == nil {
		for _, k := range keys {
			if strings.TrimPrefix(k, "template:") == name {
				return true
			}
		}
	}
	return false
}

// templateKey returns the settings key under which a template override is stored.
func templateKey(name string) string {
	return "template:" + name
}

// readTemplate returns the template content, preferring a web-edited override stored in the database over the embedded copy.
func (a *API) readTemplate(name string) (string, bool) {
	if templateNameValid(name) {
		if override, err := a.repo.GetSetting(templateKey(name)); err == nil && override != "" {
			return override, true
		}
	}
	fileBytes, err := deployFS.ReadFile("deploy/" + name)
	if err != nil {
		return "", false
	}
	return string(fileBytes), true
}

// serveNodeAgent serves the node_agent.py launcher template with the active SECRET_TOKEN injected. node_agent.py is a thin bootstrap that imports the modular agent_src package, so single-file downloads and the legacy `import node_agent` CLI contract keep working.
func (a *API) serveNodeAgent(w http.ResponseWriter, r *http.Request) {
	content, ok := a.readTemplate("node_agent.py")
	if !ok {
		http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
		log.Printf("Error reading node_agent.py template")
		return
	}

	secret := a.config.FleetSecret
	if secret == "" {
		dbSecret, err := a.repo.GetSetting("fleet_secret")
		if err != nil {
			log.Printf("ERROR: failed to read fleet_secret from DB for node agent: %v", err)
		}
		if dbSecret != "" {
			secret = dbSecret
		}
	}

	content = strings.ReplaceAll(content, "__FLEET_SECRET__", secret)
	content = a.applyDomainPlaceholders(content)
	contentBytes := stripUTF8BOM([]byte(content))

	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	w.Write(contentBytes)
}

// InstallCommandHandler returns the exact one-line command used to onboard a new node, with the join domain and fleet secret pre-filled. The secret is exposed here on purpose: the route is permission-gated (can_update_client) and only reachable through an authenticated web session.
func (a *API) InstallCommandHandler(w http.ResponseWriter, r *http.Request) {
	if a.config.FleetSecret == "" || a.config.JoinDomain == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Join domain or fleet secret is not configured"})
		return
	}

	joinBase := "https://" + a.config.JoinDomain
	command := "curl -sSL " + joinBase + "/join.sh?t=" + a.config.FleetSecret + " | bash"
	commandWindows := "irm " + joinBase + "/join.ps1?t=" + a.config.FleetSecret + " | iex"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"command":         command,
		"command_windows": commandWindows,
	})
}

// GetTemplatesHandler returns every client deployment template file stored in the embedded deploy directory (dynamically scanned — no hardcoded list), plus any extra overrides present in the DB settings table, with names and raw contents. Web-edited overrides take precedence over the embedded copies.
func (a *API) GetTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	names := listTemplateFiles()
	if keys, err := a.repo.GetSettingKeysByPrefix("template:"); err == nil {
		for _, k := range keys {
			name := strings.TrimPrefix(k, "template:")
			if templateNameValid(name) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)

	result := make([]map[string]string, 0, len(names))
	for _, name := range names {
		content, ok := a.readTemplate(name)
		if !ok {
			log.Printf("Error reading template %s", name)
			continue
		}
		result = append(result, map[string]string{"name": name, "content": content})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"files":  result,
	})
}

// UpdateTemplateHandler overwrites a client template file (validated against the dynamically scanned deploy dir / DB overrides) with the content from the request body. The new content is stored in the database (authoritative for serving) and written to the local repo deploy dir when possible. Body: {"content": "..."}
func (a *API) UpdateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	vars := mux.Vars(r)
	filename := vars["filename"]
	if !a.isKnownTemplate(filename) {
		http.Error(w, "Bad Request: unknown template file", http.StatusBadRequest)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if err := a.repo.SetSetting(templateKey(filename), req.Content); err != nil {
		log.Printf("ERROR: Failed to save template %s: %v", filename, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Best-effort write into the repo deploy dir so a source checkout stays in sync (on the server the running binary serves from the DB override).
	if err := os.WriteFile("internal/api/deploy/"+filename, []byte(req.Content), 0o644); err != nil {
		log.Printf("WARN: Could not write template %s to disk: %v", filename, err)
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateTemplate, filename, "Overwrote client template ("+strconv.Itoa(len(req.Content))+" bytes)")

	log.Printf("Template %s updated (%d bytes)", filename, len(req.Content))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Template saved",
	})
}

// serveTemplateFile serves a whitelisted client template, preferring the web-edited DB override over the embedded copy.
func (a *API) serveTemplateFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, ok := a.readTemplate(name)
		if !ok {
			http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
			log.Printf("Error reading template %s", name)
			return
		}
		contentBytes := stripUTF8BOM([]byte(content))
		if isShellScript(name) {
			contentBytes = stripCRLF(contentBytes)
		}
		w.Header().Set("Content-Type", contentType+"; charset=utf-8")
		w.Write([]byte(a.applyDomainPlaceholders(string(contentBytes))))
	}
}
