package domain

import (
	"fmt"
	"strings"
	"testing"
)

// validManifestYAML returns a minimal valid manifest YAML string.
func validManifestYAML() string {
	return `apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  namespace: default
  version: "1.0.0"
  labels:
    team: platform
spec:
  runtime: claude
  capabilities:
    - name: code-review
      type: mcp-tool
  slo:
    latency: 5000
    accuracy: 95.0
    cost: 0.05
  resources:
    maxConcurrentMissions: 10
    maxMemoryMB: 512
  deployment:
    strategy: canary
    canaryPercent: 10
    maxInstances: 5
    drainTimeout: 300s
`
}

func TestParseAndValidateManifest_ValidManifest(t *testing.T) {
	manifest, err := ParseAndValidateManifest([]byte(validManifestYAML()))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if manifest.Name != "test-agent" {
		t.Errorf("expected name test-agent, got %s", manifest.Name)
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", manifest.Version)
	}
	if manifest.RuntimeType != RuntimeClaude {
		t.Errorf("expected runtime claude, got %s", manifest.RuntimeType)
	}
	if len(manifest.Capabilities) != 1 {
		t.Errorf("expected 1 capability, got %d", len(manifest.Capabilities))
	}
	if manifest.SLOs.Latency == nil || manifest.SLOs.Latency.MaxMs != 5000 {
		t.Errorf("expected latency 5000, got %+v", manifest.SLOs.Latency)
	}
	if manifest.SLOs.Accuracy == nil || manifest.SLOs.Accuracy.MinPercent != 95.0 {
		t.Errorf("expected accuracy 95.0, got %+v", manifest.SLOs.Accuracy)
	}
	if manifest.SLOs.Cost == nil || manifest.SLOs.Cost.MaxCost != 0.05 {
		t.Errorf("expected cost 0.05, got %+v", manifest.SLOs.Cost)
	}
}

func TestParseAndValidateManifest_SizeExceeded(t *testing.T) {
	data := make([]byte, MaxManifestSize+1)
	_, err := ParseAndValidateManifest(data)
	if err == nil {
		t.Fatal("expected error for oversized manifest")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if ve.Violations[0].Type != ViolationSize {
		t.Errorf("expected size violation, got %s", ve.Violations[0].Type)
	}
}

func TestParseAndValidateManifest_InvalidYAML(t *testing.T) {
	_, err := ParseAndValidateManifest([]byte("{{invalid yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if ve.Violations[0].Type != ViolationFormat {
		t.Errorf("expected format violation, got %s", ve.Violations[0].Type)
	}
}

func TestParseAndValidateManifest_MissingName(t *testing.T) {
	yaml := `apiVersion: agentplane.io/v1
kind: Agent
metadata:
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if v.Field == "metadata.name" && v.Type == ViolationRequired {
			found = true
		}
	}
	if !found {
		t.Error("expected required violation for metadata.name")
	}
}

func TestParseAndValidateManifest_InvalidNameFormat(t *testing.T) {
	cases := []string{
		"UPPERCASE",
		"-starts-with-hyphen",
		"has spaces",
		"has_underscore",
		strings.Repeat("a", 254),
	}
	for _, name := range cases {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: %s
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, name)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err == nil {
			t.Errorf("expected error for invalid name %q", name)
			continue
		}
		ve := err.(*ValidationError)
		found := false
		for _, v := range ve.Violations {
			if v.Field == "metadata.name" && v.Type == ViolationFormat {
				found = true
			}
		}
		if !found {
			t.Errorf("expected format violation for name %q, got %+v", name, ve.Violations)
		}
	}
}

func TestParseAndValidateManifest_InvalidVersion(t *testing.T) {
	cases := []string{"1.0", "v1.0.0", "1.0.0.0", "abc"}
	for _, ver := range cases {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "%s"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, ver)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err == nil {
			t.Errorf("expected error for invalid version %q", ver)
			continue
		}
		ve := err.(*ValidationError)
		found := false
		for _, v := range ve.Violations {
			if v.Field == "metadata.version" && v.Type == ViolationFormat {
				found = true
			}
		}
		if !found {
			t.Errorf("expected format violation for version %q", ver)
		}
	}
}

func TestParseAndValidateManifest_InvalidRuntime(t *testing.T) {
	yaml := `apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: invalid-runtime
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid runtime")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if v.Field == "spec.runtime" && v.Type == ViolationEnum {
			found = true
		}
	}
	if !found {
		t.Error("expected enum violation for spec.runtime")
	}
}

func TestParseAndValidateManifest_TooManyCapabilities(t *testing.T) {
	caps := ""
	for i := 0; i <= MaxCapabilities; i++ {
		caps += fmt.Sprintf("    - name: cap-%d\n      type: mcp-tool\n", i)
	}
	yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
%s  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, caps)
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for too many capabilities")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if v.Field == "spec.capabilities" && v.Type == ViolationMaxItems {
			found = true
		}
	}
	if !found {
		t.Error("expected max_items violation for spec.capabilities")
	}
}

func TestParseAndValidateManifest_TooManyLabels(t *testing.T) {
	labels := ""
	for i := 0; i <= MaxLabels; i++ {
		labels += fmt.Sprintf("    key%d: value%d\n", i, i)
	}
	yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
  labels:
%sspec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, labels)
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for too many labels")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if v.Field == "metadata.labels" && v.Type == ViolationMaxItems {
			found = true
		}
	}
	if !found {
		t.Error("expected max_items violation for metadata.labels")
	}
}

func TestParseAndValidateManifest_LabelKeyTooLong(t *testing.T) {
	longKey := strings.Repeat("k", MaxLabelKeyLen+1)
	yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
  labels:
    %s: value
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, longKey)
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for long label key")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if strings.HasPrefix(v.Field, "metadata.labels[") && v.Type == ViolationMaxLength {
			found = true
		}
	}
	if !found {
		t.Error("expected max_length violation for label key")
	}
}

func TestParseAndValidateManifest_LabelValueTooLong(t *testing.T) {
	longValue := strings.Repeat("v", MaxLabelValueLen+1)
	yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
  labels:
    team: %s
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, longValue)
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for long label value")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if strings.HasPrefix(v.Field, "metadata.labels[") && v.Type == ViolationMaxLength {
			found = true
		}
	}
	if !found {
		t.Error("expected max_length violation for label value")
	}
}

func TestParseAndValidateManifest_SLOLatencyOutOfRange(t *testing.T) {
	cases := []int{0, -1, 60001}
	for _, lat := range cases {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: %d
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, lat)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err == nil {
			t.Errorf("expected error for latency %d", lat)
			continue
		}
		ve := err.(*ValidationError)
		found := false
		for _, v := range ve.Violations {
			if v.Field == "spec.slo.latency" && v.Type == ViolationRange {
				found = true
			}
		}
		if !found {
			t.Errorf("expected range violation for latency %d", lat)
		}
	}
}

func TestParseAndValidateManifest_SLOAccuracyOutOfRange(t *testing.T) {
	cases := []float64{-0.1, 100.1}
	for _, acc := range cases {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    accuracy: %.1f
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, acc)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err == nil {
			t.Errorf("expected error for accuracy %.1f", acc)
			continue
		}
		ve := err.(*ValidationError)
		found := false
		for _, v := range ve.Violations {
			if v.Field == "spec.slo.accuracy" && v.Type == ViolationRange {
				found = true
			}
		}
		if !found {
			t.Errorf("expected range violation for accuracy %.1f", acc)
		}
	}
}

func TestParseAndValidateManifest_SLOCostNegative(t *testing.T) {
	yaml := `apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    cost: -0.01
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for negative cost")
	}
	ve := err.(*ValidationError)
	found := false
	for _, v := range ve.Violations {
		if v.Field == "spec.slo.cost" && v.Type == ViolationRange {
			found = true
		}
	}
	if !found {
		t.Error("expected range violation for negative cost")
	}
}

func TestParseAndValidateManifest_MultipleViolations(t *testing.T) {
	// Missing name, invalid version, invalid runtime — should report all
	yaml := `apiVersion: agentplane.io/v1
kind: Agent
metadata:
  version: "not-semver"
spec:
  runtime: invalid
  capabilities: []
  slo:
    latency: 0
  resources:
    maxConcurrentMissions: 0
  deployment:
    strategy: ""
`
	_, err := ParseAndValidateManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for multiple violations")
	}
	ve := err.(*ValidationError)
	// Should have at least: missing name, bad version, bad runtime, no caps, bad latency, bad resources, bad deployment
	if len(ve.Violations) < 5 {
		t.Errorf("expected at least 5 violations, got %d: %v", len(ve.Violations), ve.Violations)
	}
}

func TestParseAndValidateManifest_AllRuntimes(t *testing.T) {
	runtimes := []string{"claude", "kiro", "bedrock", "custom"}
	for _, rt := range runtimes {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: %s
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: 1000
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, rt)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err != nil {
			t.Errorf("expected no error for runtime %q, got: %v", rt, err)
		}
	}
}

func TestParseAndValidateManifest_BoundaryLatency(t *testing.T) {
	// Min and max valid latencies
	for _, lat := range []int{1, 60000} {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    latency: %d
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, lat)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err != nil {
			t.Errorf("expected no error for latency %d, got: %v", lat, err)
		}
	}
}

func TestParseAndValidateManifest_BoundaryAccuracy(t *testing.T) {
	for _, acc := range []float64{0.0, 100.0} {
		yaml := fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: test-agent
  version: "1.0.0"
spec:
  runtime: claude
  capabilities:
    - name: test
      type: mcp-tool
  slo:
    accuracy: %.1f
  resources:
    maxConcurrentMissions: 5
  deployment:
    strategy: rolling
`, acc)
		_, err := ParseAndValidateManifest([]byte(yaml))
		if err != nil {
			t.Errorf("expected no error for accuracy %.1f, got: %v", acc, err)
		}
	}
}
