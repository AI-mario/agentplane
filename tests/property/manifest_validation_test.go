package property

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentplane/agentplane/internal/domain"
	"pgregory.net/rapid"
)

// --- Generators ---

// genValidRuntime picks a valid runtime type.
func genValidRuntime() *rapid.Generator[string] {
	return rapid.SampledFrom([]string{"claude", "kiro", "bedrock", "custom"})
}

// genInvalidRuntime generates a string NOT in the valid runtime set.
func genInvalidRuntime() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		s := rapid.StringMatching(`[a-z]{3,20}`).Draw(t, "runtime")
		for s == "claude" || s == "kiro" || s == "bedrock" || s == "custom" {
			s = s + "x"
		}
		return s
	})
}

// genCapabilities generates a YAML string of N capabilities.
func genCapabilities(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(fmt.Sprintf("    - name: cap-%d\n      type: mcp-tool\n", i))
	}
	return sb.String()
}

// genLabelsYAML generates a YAML string with N labels.
func genLabelsYAML(n int, keyLen int, valLen int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%d%s", i, strings.Repeat("x", max(0, keyLen-len(fmt.Sprintf("k%d", i)))))
		val := fmt.Sprintf("v%d%s", i, strings.Repeat("y", max(0, valLen-len(fmt.Sprintf("v%d", i)))))
		sb.WriteString(fmt.Sprintf("    %s: %s\n", key, val))
	}
	return sb.String()
}

// buildManifest constructs a manifest YAML with given parameters.
func buildManifest(name, version, runtime, capabilities, labels, slo string) string {
	labelsSection := ""
	if labels != "" {
		labelsSection = fmt.Sprintf("  labels:\n%s", labels)
	}
	return fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  name: %s
  namespace: default
  version: "%s"
%sspec:
  runtime: %s
  capabilities:
%s  slo:
%s  resources:
    maxConcurrentMissions: 10
    maxMemoryMB: 512
  deployment:
    strategy: rolling
    maxInstances: 5
    drainTimeout: 300s
`, name, version, labelsSection, runtime, capabilities, slo)
}

// validSLO returns a valid SLO YAML snippet.
func validSLO() string {
	return "    latency: 5000\n    accuracy: 95.0\n    cost: 0.05\n"
}

// --- Property Tests ---

// TestProperty4_ManifestValidationCompleteness tests that for any manifest with N violations,
// the validator returns exactly N errors with field paths.
// **Validates: Requirements 1.4**
func TestProperty4_ManifestValidationCompleteness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a manifest with a controlled set of violations.
		// We introduce violations by toggling specific fields.
		missingName := rapid.Bool().Draw(t, "missingName")
		badVersion := rapid.Bool().Draw(t, "badVersion")
		badRuntime := rapid.Bool().Draw(t, "badRuntime")
		noCaps := rapid.Bool().Draw(t, "noCaps")
		badLatency := rapid.Bool().Draw(t, "badLatency")

		// Need at least one violation to test
		if !missingName && !badVersion && !badRuntime && !noCaps && !badLatency {
			missingName = true
		}

		expectedViolations := 0

		name := "valid-agent"
		if missingName {
			name = ""
			expectedViolations++
		}

		version := "1.0.0"
		if badVersion {
			version = "not-semver"
			expectedViolations++
		}

		runtime := "claude"
		if badRuntime {
			runtime = "invalid-rt"
			expectedViolations++
		}

		capsYAML := "    - name: test-cap\n      type: mcp-tool\n"
		if noCaps {
			capsYAML = ""
			expectedViolations++
		}

		slo := "    latency: 5000\n"
		if badLatency {
			slo = "    latency: 0\n"
			expectedViolations++
		}

		// Build the manifest without labels section if name is empty
		labelsSection := ""
		var yaml string
		if name == "" {
			yaml = fmt.Sprintf(`apiVersion: agentplane.io/v1
kind: Agent
metadata:
  namespace: default
  version: "%s"
spec:
  runtime: %s
  capabilities:
%s  slo:
%s  resources:
    maxConcurrentMissions: 10
    maxMemoryMB: 512
  deployment:
    strategy: rolling
`, version, runtime, capsYAML, slo)
		} else {
			yaml = buildManifest(name, version, runtime, capsYAML, labelsSection, slo)
		}

		_, err := domain.ParseAndValidateManifest([]byte(yaml))
		if err == nil {
			t.Fatal("expected validation error but got nil")
		}

		ve, ok := err.(*domain.ValidationError)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T", err)
		}

		if len(ve.Violations) != expectedViolations {
			t.Errorf("expected %d violations, got %d: %v", expectedViolations, len(ve.Violations), ve.Violations)
		}

		// Every violation must have a non-empty Field path
		for _, v := range ve.Violations {
			if v.Field == "" {
				t.Errorf("violation has empty field path: %v", v)
			}
		}
	})
}

// TestProperty6_CapabilityAndLabelBoundsEnforcement tests that registrations with
// capabilities > 100, labels > 50, key > 64 chars, or value > 256 chars are rejected.
// **Validates: Requirements 1.6, 1.7**
func TestProperty6_CapabilityAndLabelBoundsEnforcement(t *testing.T) {
	t.Run("TooManyCapabilities", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			numCaps := rapid.IntRange(domain.MaxCapabilities+1, domain.MaxCapabilities+50).Draw(t, "numCaps")
			capsYAML := genCapabilities(numCaps)
			yaml := buildManifest("valid-agent", "1.0.0", "claude", capsYAML, "", validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatal("expected rejection for too many capabilities")
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "spec.capabilities" && v.Type == domain.ViolationMaxItems {
					found = true
				}
			}
			if !found {
				t.Errorf("expected max_items violation for capabilities, got: %v", ve.Violations)
			}
		})
	})

	t.Run("TooManyLabels", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			numLabels := rapid.IntRange(domain.MaxLabels+1, domain.MaxLabels+20).Draw(t, "numLabels")
			labelsYAML := genLabelsYAML(numLabels, 10, 10)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", labelsYAML, validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatal("expected rejection for too many labels")
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "metadata.labels" && v.Type == domain.ViolationMaxItems {
					found = true
				}
			}
			if !found {
				t.Errorf("expected max_items violation for labels, got: %v", ve.Violations)
			}
		})
	})

	t.Run("LabelKeyTooLong", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			keyLen := rapid.IntRange(domain.MaxLabelKeyLen+1, domain.MaxLabelKeyLen+50).Draw(t, "keyLen")
			longKey := strings.Repeat("k", keyLen)
			labelsYAML := fmt.Sprintf("    %s: value\n", longKey)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", labelsYAML, validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatal("expected rejection for label key too long")
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if strings.HasPrefix(v.Field, "metadata.labels[") && v.Type == domain.ViolationMaxLength {
					found = true
				}
			}
			if !found {
				t.Errorf("expected max_length violation for label key, got: %v", ve.Violations)
			}
		})
	})

	t.Run("LabelValueTooLong", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			valLen := rapid.IntRange(domain.MaxLabelValueLen+1, domain.MaxLabelValueLen+50).Draw(t, "valLen")
			longVal := strings.Repeat("v", valLen)
			labelsYAML := fmt.Sprintf("    mykey: %s\n", longVal)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", labelsYAML, validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatal("expected rejection for label value too long")
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if strings.HasPrefix(v.Field, "metadata.labels[") && v.Type == domain.ViolationMaxLength {
					found = true
				}
			}
			if !found {
				t.Errorf("expected max_length violation for label value, got: %v", ve.Violations)
			}
		})
	})

	t.Run("ValidBoundsRoundTrip", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			numCaps := rapid.IntRange(1, domain.MaxCapabilities).Draw(t, "numCaps")
			numLabels := rapid.IntRange(0, domain.MaxLabels).Draw(t, "numLabels")

			capsYAML := genCapabilities(numCaps)
			labelsYAML := ""
			if numLabels > 0 {
				labelsYAML = genLabelsYAML(numLabels, 10, 10)
			}
			yaml := buildManifest("valid-agent", "1.0.0", "claude", capsYAML, labelsYAML, validSLO())

			manifest, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err != nil {
				t.Fatalf("expected valid manifest, got error: %v", err)
			}
			if len(manifest.Capabilities) != numCaps {
				t.Errorf("expected %d capabilities, got %d", numCaps, len(manifest.Capabilities))
			}
			if numLabels > 0 && len(manifest.Labels) != numLabels {
				t.Errorf("expected %d labels, got %d", numLabels, len(manifest.Labels))
			}
		})
	})
}

// TestProperty7_ManifestSizeAndRequiredFieldsValidation tests that manifests exceeding 1MB
// are rejected, as well as manifests missing required fields or with invalid name/version patterns.
// **Validates: Requirements 2.1, 2.2**
func TestProperty7_ManifestSizeAndRequiredFieldsValidation(t *testing.T) {
	t.Run("OversizedManifest", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate data slightly over 1MB
			extraBytes := rapid.IntRange(1, 1024).Draw(t, "extraBytes")
			data := make([]byte, domain.MaxManifestSize+extraBytes)
			// Fill with valid-ish YAML characters
			for i := range data {
				data[i] = 'a'
			}

			_, err := domain.ParseAndValidateManifest(data)
			if err == nil {
				t.Fatal("expected rejection for oversized manifest")
			}
			ve, ok := err.(*domain.ValidationError)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			found := false
			for _, v := range ve.Violations {
				if v.Type == domain.ViolationSize {
					found = true
				}
			}
			if !found {
				t.Errorf("expected size violation, got: %v", ve.Violations)
			}
		})
	})

	t.Run("MissingRequiredFields", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Choose which required fields to omit (at least one)
			omitName := rapid.Bool().Draw(t, "omitName")
			omitVersion := rapid.Bool().Draw(t, "omitVersion")
			omitRuntime := rapid.Bool().Draw(t, "omitRuntime")
			omitCaps := rapid.Bool().Draw(t, "omitCaps")
			omitSLO := rapid.Bool().Draw(t, "omitSLO")
			omitDeployment := rapid.Bool().Draw(t, "omitDeployment")

			if !omitName && !omitVersion && !omitRuntime && !omitCaps && !omitSLO && !omitDeployment {
				omitName = true // ensure at least one omission
			}

			var sb strings.Builder
			sb.WriteString("apiVersion: agentplane.io/v1\nkind: Agent\nmetadata:\n")
			if !omitName {
				sb.WriteString("  name: valid-agent\n")
			}
			sb.WriteString("  namespace: default\n")
			if !omitVersion {
				sb.WriteString("  version: \"1.0.0\"\n")
			}
			sb.WriteString("spec:\n")
			if !omitRuntime {
				sb.WriteString("  runtime: claude\n")
			}
			if !omitCaps {
				sb.WriteString("  capabilities:\n    - name: test-cap\n      type: mcp-tool\n")
			}
			if !omitSLO {
				sb.WriteString("  slo:\n    latency: 5000\n")
			}
			sb.WriteString("  resources:\n    maxConcurrentMissions: 10\n    maxMemoryMB: 512\n")
			if !omitDeployment {
				sb.WriteString("  deployment:\n    strategy: rolling\n")
			}

			_, err := domain.ParseAndValidateManifest([]byte(sb.String()))
			if err == nil {
				t.Fatal("expected rejection for missing required fields")
			}
			ve, ok := err.(*domain.ValidationError)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if len(ve.Violations) == 0 {
				t.Error("expected at least one violation")
			}
			// Every violation should have a Type indicating required or similar
			for _, v := range ve.Violations {
				if v.Type == "" {
					t.Errorf("violation missing type: %v", v)
				}
			}
		})
	})

	t.Run("InvalidNamePattern", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate names that violate the pattern
			kind := rapid.IntRange(0, 3).Draw(t, "kind")
			var name string
			switch kind {
			case 0: // starts with hyphen
				name = "-" + strings.Repeat("a", rapid.IntRange(1, 10).Draw(t, "len"))
			case 1: // uppercase
				name = "A" + strings.Repeat("a", rapid.IntRange(1, 10).Draw(t, "len"))
			case 2: // too long (>253)
				name = strings.Repeat("a", rapid.IntRange(254, 300).Draw(t, "len"))
			case 3: // contains invalid chars
				name = "valid" + string(rune(rapid.IntRange(33, 44).Draw(t, "badChar"))) + "name"
			}

			yaml := buildManifest(name, "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatalf("expected rejection for invalid name %q", name)
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "metadata.name" && v.Type == domain.ViolationFormat {
					found = true
				}
			}
			if !found {
				t.Errorf("expected format violation for name %q, got: %v", name, ve.Violations)
			}
		})
	})

	t.Run("InvalidVersionPattern", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			kind := rapid.IntRange(0, 3).Draw(t, "kind")
			var version string
			switch kind {
			case 0: // only two parts
				version = fmt.Sprintf("%d.%d", rapid.IntRange(0, 10).Draw(t, "a"), rapid.IntRange(0, 10).Draw(t, "b"))
			case 1: // four parts
				version = fmt.Sprintf("%d.%d.%d.%d",
					rapid.IntRange(0, 10).Draw(t, "a"), rapid.IntRange(0, 10).Draw(t, "b"),
					rapid.IntRange(0, 10).Draw(t, "c"), rapid.IntRange(0, 10).Draw(t, "d"))
			case 2: // leading v
				version = fmt.Sprintf("v%d.%d.%d",
					rapid.IntRange(0, 10).Draw(t, "a"), rapid.IntRange(0, 10).Draw(t, "b"), rapid.IntRange(0, 10).Draw(t, "c"))
			case 3: // non-numeric
				version = "abc.def.ghi"
			}

			yaml := buildManifest("valid-agent", version, "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatalf("expected rejection for invalid version %q", version)
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "metadata.version" && v.Type == domain.ViolationFormat {
					found = true
				}
			}
			if !found {
				t.Errorf("expected format violation for version %q, got: %v", version, ve.Violations)
			}
		})
	})
}

// TestProperty8_RuntimeTypeEnumValidation tests that any runtime value not in
// {claude, kiro, bedrock, custom} causes manifest rejection.
// **Validates: Requirements 2.4**
func TestProperty8_RuntimeTypeEnumValidation(t *testing.T) {
	t.Run("InvalidRuntimeRejected", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			runtime := genInvalidRuntime().Draw(t, "runtime")

			yaml := buildManifest("valid-agent", "1.0.0", runtime,
				"    - name: test-cap\n      type: mcp-tool\n", "", validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatalf("expected rejection for invalid runtime %q", runtime)
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "spec.runtime" && v.Type == domain.ViolationEnum {
					found = true
				}
			}
			if !found {
				t.Errorf("expected enum violation for runtime %q, got: %v", runtime, ve.Violations)
			}
		})
	})

	t.Run("ValidRuntimeAccepted", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			runtime := genValidRuntime().Draw(t, "runtime")

			yaml := buildManifest("valid-agent", "1.0.0", runtime,
				"    - name: test-cap\n      type: mcp-tool\n", "", validSLO())

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err != nil {
				t.Fatalf("expected valid manifest for runtime %q, got error: %v", runtime, err)
			}
		})
	})
}

// TestProperty39_SLODefinitionValidation tests that latency outside [1, 60000],
// accuracy outside [0.0, 100.0], and negative cost are rejected.
// **Validates: Requirements 9.1, 9.6**
func TestProperty39_SLODefinitionValidation(t *testing.T) {
	t.Run("InvalidLatency", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate latency outside [1, 60000]
			belowOrAbove := rapid.Bool().Draw(t, "below")
			var latency int
			if belowOrAbove {
				latency = rapid.IntRange(-1000, 0).Draw(t, "latency")
			} else {
				latency = rapid.IntRange(60001, 120000).Draw(t, "latency")
			}

			slo := fmt.Sprintf("    latency: %d\n", latency)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", slo)

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatalf("expected rejection for latency %d", latency)
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "spec.slo.latency" && v.Type == domain.ViolationRange {
					found = true
				}
			}
			if !found {
				t.Errorf("expected range violation for latency %d, got: %v", latency, ve.Violations)
			}
		})
	})

	t.Run("ValidLatency", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			latency := rapid.IntRange(1, 60000).Draw(t, "latency")
			slo := fmt.Sprintf("    latency: %d\n", latency)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", slo)

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err != nil {
				t.Fatalf("expected valid manifest for latency %d, got: %v", latency, err)
			}
		})
	})

	t.Run("InvalidAccuracy", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			belowOrAbove := rapid.Bool().Draw(t, "below")
			var accuracy float64
			if belowOrAbove {
				accuracy = rapid.Float64Range(-1000.0, -0.01).Draw(t, "accuracy")
			} else {
				accuracy = rapid.Float64Range(100.01, 1000.0).Draw(t, "accuracy")
			}

			slo := fmt.Sprintf("    accuracy: %f\n", accuracy)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", slo)

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatalf("expected rejection for accuracy %f", accuracy)
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "spec.slo.accuracy" && v.Type == domain.ViolationRange {
					found = true
				}
			}
			if !found {
				t.Errorf("expected range violation for accuracy %f, got: %v", accuracy, ve.Violations)
			}
		})
	})

	t.Run("ValidAccuracy", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			accuracy := rapid.Float64Range(0.0, 100.0).Draw(t, "accuracy")
			slo := fmt.Sprintf("    accuracy: %f\n", accuracy)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", slo)

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err != nil {
				t.Fatalf("expected valid manifest for accuracy %f, got: %v", accuracy, err)
			}
		})
	})

	t.Run("NegativeCost", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			cost := rapid.Float64Range(-1000.0, -0.0001).Draw(t, "cost")
			slo := fmt.Sprintf("    cost: %f\n", cost)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", slo)

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err == nil {
				t.Fatalf("expected rejection for negative cost %f", cost)
			}
			ve := err.(*domain.ValidationError)
			found := false
			for _, v := range ve.Violations {
				if v.Field == "spec.slo.cost" && v.Type == domain.ViolationRange {
					found = true
				}
			}
			if !found {
				t.Errorf("expected range violation for cost %f, got: %v", cost, ve.Violations)
			}
		})
	})

	t.Run("ValidCost", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			cost := rapid.Float64Range(0.0, 10000.0).Draw(t, "cost")
			slo := fmt.Sprintf("    cost: %f\n", cost)
			yaml := buildManifest("valid-agent", "1.0.0", "claude",
				"    - name: test-cap\n      type: mcp-tool\n", "", slo)

			_, err := domain.ParseAndValidateManifest([]byte(yaml))
			if err != nil {
				t.Fatalf("expected valid manifest for cost %f, got: %v", cost, err)
			}
		})
	})
}
