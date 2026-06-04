package property

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"
)

// --- Property 57: CLI Output Format Validity ---
// For any CLI command output, when --output=json the output SHALL be valid parseable JSON,
// when --output=yaml the output SHALL be valid parseable YAML, and when no flag is specified
// the output SHALL be in table format.
// **Validates: Requirements 14.4**

// genJSONResponse generates random valid JSON objects that the API might return.
func genJSONResponse() *rapid.Generator[[]byte] {
	return rapid.Custom(func(t *rapid.T) []byte {
		// Generate a random API-like response object.
		numFields := rapid.IntRange(1, 5).Draw(t, "numFields")
		obj := make(map[string]interface{})
		for i := 0; i < numFields; i++ {
			key := rapid.SampledFrom([]string{
				"id", "name", "status", "version", "runtime",
				"count", "message", "error", "team", "namespace",
			}).Draw(t, fmt.Sprintf("key_%d", i))
			// Generate various value types.
			valType := rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("valType_%d", i))
			switch valType {
			case 0:
				obj[key] = rapid.StringMatching(`[a-z0-9\-]{1,20}`).Draw(t, fmt.Sprintf("strVal_%d", i))
			case 1:
				obj[key] = rapid.IntRange(0, 10000).Draw(t, fmt.Sprintf("intVal_%d", i))
			case 2:
				obj[key] = rapid.Bool().Draw(t, fmt.Sprintf("boolVal_%d", i))
			}
		}
		data, _ := json.Marshal(obj)
		return data
	})
}

// buildCLIBinary builds the apctl binary for testing and returns its path.
func buildCLIBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "apctl")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/apctl")
	cmd.Dir = findModuleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build apctl binary: %v\n%s", err, out)
	}
	return binary
}

// findModuleRoot locates the module root directory.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	// Walk up from test file location to find go.mod.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (go.mod)")
		}
		dir = parent
	}
}

func TestProperty57_CLIOutputFormat_JSONValid(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Create a mock server that returns random JSON.
		responseData := genJSONResponse().Draw(rt, "response")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(responseData)
		}))
		defer srv.Close()

		// Run CLI with --output=json against mock server.
		cmd := exec.Command(binary, "agent", "describe", "test-agent", "--output", "json")
		cmd.Env = append(os.Environ(),
			"AGENTPLANE_ENDPOINT="+srv.URL,
			"AGENTPLANE_TOKEN=test-token",
			"HOME="+t.TempDir(),
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("CLI command failed: %v", err)
		}

		// Output must be valid JSON.
		trimmed := strings.TrimSpace(string(out))
		if !json.Valid([]byte(trimmed)) {
			t.Fatalf("--output=json produced invalid JSON:\n%s", trimmed)
		}
	})
}

func TestProperty57_CLIOutputFormat_YAMLValid(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Create a mock server that returns random JSON.
		responseData := genJSONResponse().Draw(rt, "response")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(responseData)
		}))
		defer srv.Close()

		// Run CLI with --output=yaml against mock server.
		cmd := exec.Command(binary, "agent", "describe", "test-agent", "--output", "yaml")
		cmd.Env = append(os.Environ(),
			"AGENTPLANE_ENDPOINT="+srv.URL,
			"AGENTPLANE_TOKEN=test-token",
			"HOME="+t.TempDir(),
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("CLI command failed: %v", err)
		}

		// Output must be valid YAML.
		trimmed := strings.TrimSpace(string(out))
		var parsed interface{}
		if err := yaml.Unmarshal([]byte(trimmed), &parsed); err != nil {
			t.Fatalf("--output=yaml produced invalid YAML:\n%s\nerror: %v", trimmed, err)
		}
		// YAML output should parse back to a non-nil value for non-empty responses.
		if parsed == nil {
			t.Fatalf("--output=yaml parsed to nil for response: %s", string(responseData))
		}
	})
}

func TestProperty57_CLIOutputFormat_DefaultIsTable(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Create a mock server that returns a list of agents (exercises table rendering).
		agentCount := rapid.IntRange(0, 5).Draw(rt, "agentCount")
		agents := make([]map[string]interface{}, agentCount)
		for i := 0; i < agentCount; i++ {
			agents[i] = map[string]interface{}{
				"id":          fmt.Sprintf("agent-%d", i),
				"name":        rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, fmt.Sprintf("name_%d", i)),
				"version":     "1.0.0",
				"runtimeType": "claude",
				"status":      "active",
			}
		}
		respObj := map[string]interface{}{
			"agents": agents,
			"count":  agentCount,
		}
		responseData, _ := json.Marshal(respObj)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(responseData)
		}))
		defer srv.Close()

		// Run CLI with default output (no --output flag) using agent list.
		cmd := exec.Command(binary, "agent", "list")
		cmd.Env = append(os.Environ(),
			"AGENTPLANE_ENDPOINT="+srv.URL,
			"AGENTPLANE_TOKEN=test-token",
			"HOME="+t.TempDir(),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI command failed: %v\noutput: %s", err, string(out))
		}

		// Default output should NOT be valid JSON (table format).
		trimmed := strings.TrimSpace(string(out))
		if agentCount > 0 {
			// Table output contains headers with tab-separated columns.
			if !strings.Contains(trimmed, "ID") || !strings.Contains(trimmed, "NAME") {
				t.Fatalf("default output missing table headers:\n%s", trimmed)
			}
			// Should NOT be valid JSON when there are agents.
			if json.Valid([]byte(trimmed)) {
				t.Fatalf("default output should be table format, not JSON:\n%s", trimmed)
			}
		}
	})
}

// --- Property 58: CLI Configuration Precedence ---
// For any configuration parameter specified in both environment variable and config file,
// the CLI SHALL use the environment variable value.
// **Validates: Requirements 14.5**

// genEndpointURL generates random valid endpoint URLs.
func genEndpointURL() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		port := rapid.IntRange(1024, 65535).Draw(t, "port")
		return fmt.Sprintf("http://localhost:%d", port)
	})
}

// genToken generates random token strings.
func genToken() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return rapid.StringMatching(`[a-zA-Z0-9]{8,32}`).Draw(t, "token")
	})
}

func TestProperty58_CLIConfigPrecedence_EnvOverridesFile(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Generate distinct values for file vs env.
		fileEndpoint := genEndpointURL().Draw(rt, "fileEndpoint")
		envEndpoint := genEndpointURL().Draw(rt, "envEndpoint")
		fileToken := genToken().Draw(rt, "fileToken")
		envToken := genToken().Draw(rt, "envToken")

		// Ensure values are different so we can distinguish which is used.
		if fileEndpoint == envEndpoint {
			envEndpoint = envEndpoint + "1"
		}

		// Create config file in temp home dir.
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, ".agentplane")
		os.MkdirAll(configDir, 0755)
		configContent := fmt.Sprintf("endpoint: %s\ntoken: %s\n", fileEndpoint, fileToken)
		os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configContent), 0644)

		// Create a mock server that echoes back info about which endpoint was reached.
		// The env endpoint's server should be the one that gets hit.
		envSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check that the token from env was used.
			auth := r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"reached":  "env-server",
				"authUsed": auth,
			})
		}))
		defer envSrv.Close()

		// Run CLI pointing env to the mock server (overriding file config).
		cmd := exec.Command(binary, "agent", "describe", "test-agent", "--output", "json")
		cmd.Env = append(os.Environ(),
			"AGENTPLANE_ENDPOINT="+envSrv.URL,
			"AGENTPLANE_TOKEN="+envToken,
			"HOME="+homeDir,
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("CLI command failed: %v", err)
		}

		// Verify env token was used (not file token).
		var resp map[string]interface{}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("failed to parse output: %v\nraw: %s", err, string(out))
		}
		authUsed, _ := resp["authUsed"].(string)
		expectedAuth := "ApiKey " + envToken
		if authUsed != expectedAuth {
			t.Fatalf("expected env token in auth header %q, got %q", expectedAuth, authUsed)
		}
	})
}

func TestProperty58_CLIConfigPrecedence_FileUsedWhenNoEnv(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		fileToken := genToken().Draw(rt, "fileToken")

		// Create a mock server to capture the request.
		var capturedAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}))
		defer srv.Close()

		// Create config file with token and server endpoint.
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, ".agentplane")
		os.MkdirAll(configDir, 0755)
		configContent := fmt.Sprintf("endpoint: %s\ntoken: %s\n", srv.URL, fileToken)
		os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configContent), 0644)

		// Run CLI WITHOUT env vars — should use file config.
		cmd := exec.Command(binary, "agent", "describe", "test-agent", "--output", "json")
		// Build clean environment without AGENTPLANE_ vars.
		cleanEnv := []string{"HOME=" + homeDir, "PATH=" + os.Getenv("PATH")}
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "AGENTPLANE_") && !strings.HasPrefix(e, "HOME=") {
				cleanEnv = append(cleanEnv, e)
			}
		}
		cmd.Env = cleanEnv
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("CLI command failed: %v", err)
		}

		// Verify the file token was used.
		_ = out // output confirms command succeeded
		expectedAuth := "ApiKey " + fileToken
		if capturedAuth != expectedAuth {
			t.Fatalf("expected file token in auth header %q, got %q", expectedAuth, capturedAuth)
		}
	})
}

// --- Property 59: CLI Error Handling ---
// For any CLI command that fails, the CLI SHALL display an error message indicating the
// failure reason and exit with a non-zero status code. If endpoint URL or auth token is
// missing, the CLI SHALL error without sending a network request.
// **Validates: Requirements 14.6, 14.7**

func TestProperty59_CLIErrorHandling_MissingConfig_NoNetworkRequest(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Randomly choose which config to omit.
		missingType := rapid.SampledFrom([]string{"endpoint", "token", "both"}).Draw(rt, "missingType")

		// Track if server is hit — should never be.
		serverHit := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverHit = true
			w.WriteHeader(200)
		}))
		defer srv.Close()

		// Pick a random command.
		subcommand := rapid.SampledFrom([][]string{
			{"agent", "list"},
			{"agent", "describe", "some-id"},
			{"fleet", "status"},
			{"mission", "status", "some-id"},
		}).Draw(rt, "subcommand")

		homeDir := t.TempDir()
		// Build env based on what we're omitting.
		env := []string{
			"HOME=" + homeDir,
			"PATH=" + os.Getenv("PATH"),
		}
		// Remove any AGENTPLANE_ vars from parent env.
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "AGENTPLANE_") && !strings.HasPrefix(e, "HOME=") && !strings.HasPrefix(e, "PATH=") {
				env = append(env, e)
			}
		}

		switch missingType {
		case "endpoint":
			env = append(env, "AGENTPLANE_TOKEN=valid-token")
			// no AGENTPLANE_ENDPOINT
		case "token":
			env = append(env, "AGENTPLANE_ENDPOINT="+srv.URL)
			// no AGENTPLANE_TOKEN
		case "both":
			// neither set
		}

		cmd := exec.Command(binary, subcommand...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()

		// Must exit with non-zero.
		if err == nil {
			t.Fatalf("expected non-zero exit for missing %s, but got success.\nOutput: %s",
				missingType, string(out))
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("expected ExitError, got: %v", err)
		}
		if exitErr.ExitCode() == 0 {
			t.Fatalf("expected non-zero exit code, got 0")
		}

		// Must contain error message.
		output := string(out)
		if !strings.Contains(strings.ToLower(output), "error") &&
			!strings.Contains(strings.ToLower(output), "missing") {
			t.Fatalf("expected error message in output for missing %s, got:\n%s",
				missingType, output)
		}

		// Must NOT have hit the server.
		if serverHit {
			t.Fatalf("server was hit despite missing config (%s) — should error before network request",
				missingType)
		}
	})
}

func TestProperty59_CLIErrorHandling_APIError_NonZeroExit(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Generate random HTTP error status codes.
		statusCode := rapid.SampledFrom([]int{400, 401, 403, 404, 500, 502, 503}).Draw(rt, "statusCode")
		errorMsg := rapid.StringMatching(`[a-z ]{5,30}`).Draw(rt, "errorMsg")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			json.NewEncoder(w).Encode(map[string]string{"error": errorMsg})
		}))
		defer srv.Close()

		// Pick a random command.
		subcommand := rapid.SampledFrom([][]string{
			{"agent", "list"},
			{"agent", "describe", "some-id"},
			{"fleet", "status"},
			{"mission", "status", "some-id"},
		}).Draw(rt, "subcommand")

		cmd := exec.Command(binary, subcommand...)
		cmd.Env = append(os.Environ(),
			"AGENTPLANE_ENDPOINT="+srv.URL,
			"AGENTPLANE_TOKEN=test-token",
			"HOME="+t.TempDir(),
		)
		out, err := cmd.CombinedOutput()

		// Must exit with non-zero for API errors.
		if err == nil {
			t.Fatalf("expected non-zero exit for HTTP %d, but got success.\nOutput: %s",
				statusCode, string(out))
		}

		// Must contain the error message from the API.
		output := string(out)
		if !strings.Contains(output, errorMsg) {
			t.Fatalf("expected error message %q in output for HTTP %d, got:\n%s",
				errorMsg, statusCode, output)
		}
	})
}

func TestProperty59_CLIErrorHandling_ConnectionRefused_NonZeroExit(t *testing.T) {
	binary := buildCLIBinary(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Use a port that nothing is listening on.
		port := rapid.IntRange(49152, 65530).Draw(rt, "port")
		deadEndpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

		subcommand := rapid.SampledFrom([][]string{
			{"agent", "list"},
			{"agent", "describe", "some-id"},
		}).Draw(rt, "subcommand")

		cmd := exec.Command(binary, subcommand...)
		cmd.Env = append(os.Environ(),
			"AGENTPLANE_ENDPOINT="+deadEndpoint,
			"AGENTPLANE_TOKEN=test-token",
			"HOME="+t.TempDir(),
		)
		out, err := cmd.CombinedOutput()

		// Must exit non-zero.
		if err == nil {
			t.Fatalf("expected non-zero exit for connection refused, but got success.\nOutput: %s",
				string(out))
		}

		// Output must contain an error indication.
		output := strings.ToLower(string(out))
		if !strings.Contains(output, "error") && !strings.Contains(output, "refused") &&
			!strings.Contains(output, "failed") {
			t.Fatalf("expected error message for connection failure, got:\n%s", string(out))
		}
	})
}
