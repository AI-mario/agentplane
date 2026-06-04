// Package gemini provides an adapter for Google Gemini / Vertex AI agents.
// It translates AgentPlane A2A missions into Gemini API calls and reports
// usage metrics back to the Cost Controller.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Config holds Gemini adapter configuration.
type Config struct {
	// APIKey for generativelanguage.googleapis.com (simpler auth).
	// Leave empty to use ServiceAccountJSON for Vertex AI.
	APIKey string

	// ServiceAccountJSON is the path to a GCP service account key file.
	// Used for Vertex AI authentication via OAuth2.
	ServiceAccountJSON string

	// ProjectID is the GCP project (required for Vertex AI).
	ProjectID string

	// Location is the GCP region (e.g., "us-central1"). Required for Vertex AI.
	Location string

	// Model is the Gemini model name (e.g., "gemini-2.5-pro", "gemini-2.5-flash").
	Model string

	// Endpoint override. Defaults:
	//   API Key mode:  https://generativelanguage.googleapis.com/v1beta
	//   Vertex mode:   https://{location}-aiplatform.googleapis.com/v1
	Endpoint string

	// Timeout per request. Default 60s.
	Timeout time.Duration

	// MaxRetries on transient errors (429, 500, 503). Default 3.
	MaxRetries int
}

// Adapter implements the Gemini runtime adapter.
type Adapter struct {
	config     Config
	client     *http.Client
	mu         sync.RWMutex
	healthy    bool
	lastCheck  time.Time
}

// New creates a new Gemini adapter with the given configuration.
func New(cfg Config) *Adapter {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Model == "" {
		cfg.Model = "gemini-2.5-pro"
	}

	return &Adapter{
		config:  cfg,
		client:  &http.Client{Timeout: cfg.Timeout},
		healthy: true,
	}
}

// --- Request/Response Types ---

// GenerateRequest is the Gemini generateContent request body.
type GenerateRequest struct {
	Contents         []Content        `json:"contents"`
	Tools            []Tool           `json:"tools,omitempty"`
	GenerationConfig *GenerationConfig `json:"generationConfig,omitempty"`
	SystemInstruction *Content         `json:"systemInstruction,omitempty"`
}

// Content represents a conversation turn.
type Content struct {
	Role  string `json:"role,omitempty"` // "user" or "model"
	Parts []Part `json:"parts"`
}

// Part is a content part (text, function call, etc.)
type Part struct {
	Text         string        `json:"text,omitempty"`
	FunctionCall *FunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

// FunctionCall represents a tool/function call from the model.
type FunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// FunctionResponse represents the result of a function call.
type FunctionResponse struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
}

// Tool declares available functions for the model.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations"`
}

// FunctionDeclaration describes a callable function.
type FunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// GenerationConfig controls generation parameters.
type GenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

// GenerateResponse is the Gemini generateContent response.
type GenerateResponse struct {
	Candidates    []Candidate    `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata"`
}

// Candidate is a single response candidate.
type Candidate struct {
	Content       Content `json:"content"`
	FinishReason  string  `json:"finishReason"`
}

// UsageMetadata contains token usage information for cost metering.
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// --- Core Methods ---

// MissionPayload is the AgentPlane mission translated for Gemini.
type MissionPayload struct {
	Goal        string            `json:"goal"`
	Context     map[string]string `json:"context,omitempty"`
	Tools       []FunctionDeclaration `json:"tools,omitempty"`
	MaxTokens   int               `json:"maxTokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
}

// MissionResult is the response from Gemini translated back for AgentPlane.
type MissionResult struct {
	Response      string          `json:"response"`
	FunctionCalls []FunctionCall  `json:"functionCalls,omitempty"`
	TokensUsed    UsageMetadata   `json:"tokensUsed"`
	LatencyMs     int64           `json:"latencyMs"`
	FinishReason  string          `json:"finishReason"`
}

// Execute dispatches a mission payload to Gemini and returns the result.
func (a *Adapter) Execute(ctx context.Context, payload MissionPayload) (*MissionResult, error) {
	start := time.Now()

	// Build request
	req := &GenerateRequest{
		Contents: []Content{
			{
				Role:  "user",
				Parts: []Part{{Text: payload.Goal}},
			},
		},
	}

	// Add system instruction if context provided
	if len(payload.Context) > 0 {
		contextText := "Context:\n"
		for k, v := range payload.Context {
			contextText += fmt.Sprintf("- %s: %s\n", k, v)
		}
		req.SystemInstruction = &Content{
			Parts: []Part{{Text: contextText}},
		}
	}

	// Add tools if declared
	if len(payload.Tools) > 0 {
		req.Tools = []Tool{{FunctionDeclarations: payload.Tools}}
	}

	// Generation config
	if payload.MaxTokens > 0 || payload.Temperature > 0 {
		cfg := &GenerationConfig{}
		if payload.MaxTokens > 0 {
			cfg.MaxOutputTokens = &payload.MaxTokens
		}
		if payload.Temperature > 0 {
			cfg.Temperature = &payload.Temperature
		}
		req.GenerationConfig = cfg
	}

	// Call Gemini API with retries
	var resp *GenerateResponse
	var err error
	for attempt := 0; attempt <= a.config.MaxRetries; attempt++ {
		resp, err = a.callAPI(ctx, req)
		if err == nil {
			break
		}
		if !isRetryable(err) {
			break
		}
		// Exponential backoff: 1s, 2s, 4s (capped at 5s per spec)
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	if err != nil {
		a.setHealthy(false)
		return nil, fmt.Errorf("gemini adapter: %w", err)
	}

	a.setHealthy(true)
	latency := time.Since(start).Milliseconds()

	// Parse response
	result := &MissionResult{
		LatencyMs: latency,
	}

	if resp.UsageMetadata != nil {
		result.TokensUsed = *resp.UsageMetadata
	}

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		result.FinishReason = candidate.FinishReason

		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				result.Response += part.Text
			}
			if part.FunctionCall != nil {
				result.FunctionCalls = append(result.FunctionCalls, *part.FunctionCall)
			}
		}
	}

	return result, nil
}

// HealthCheck verifies connectivity to Gemini API.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	url := a.buildURL("/models")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	a.addAuth(req)

	resp, err := a.client.Do(req)
	if err != nil {
		a.setHealthy(false)
		return fmt.Errorf("gemini health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		a.setHealthy(false)
		return fmt.Errorf("gemini health check returned %d", resp.StatusCode)
	}

	a.setHealthy(true)
	a.mu.Lock()
	a.lastCheck = time.Now()
	a.mu.Unlock()
	return nil
}

// IsHealthy returns the last known health status.
func (a *Adapter) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.healthy
}

// --- Internal ---

func (a *Adapter) callAPI(ctx context.Context, genReq *GenerateRequest) (*GenerateResponse, error) {
	body, err := json.Marshal(genReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := a.buildURL(fmt.Sprintf("/models/%s:generateContent", a.config.Model))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	a.addAuth(req)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	var genResp GenerateResponse
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &genResp, nil
}

func (a *Adapter) buildURL(path string) string {
	if a.config.Endpoint != "" {
		return a.config.Endpoint + path
	}
	if a.config.APIKey != "" {
		return "https://generativelanguage.googleapis.com/v1beta" + path
	}
	// Vertex AI endpoint
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google",
		a.config.Location, a.config.ProjectID, a.config.Location) + path
}

func (a *Adapter) addAuth(req *http.Request) {
	if a.config.APIKey != "" {
		q := req.URL.Query()
		q.Set("key", a.config.APIKey)
		req.URL.RawQuery = q.Encode()
	}
	// For Vertex AI with service account, you'd use oauth2 token here.
	// In production, use google.DefaultTokenSource or workload identity.
}

func (a *Adapter) setHealthy(h bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.healthy = h
}

// APIError represents a non-200 response from the Gemini API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gemini API error %d: %s", e.StatusCode, e.Body)
}

func isRetryable(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == 429 || apiErr.StatusCode >= 500
	}
	return false
}
