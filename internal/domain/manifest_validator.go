package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MaxManifestSize is the maximum allowed manifest size in bytes (1 MB).
const MaxManifestSize = 1 * 1024 * 1024

// MaxCapabilities is the maximum number of capabilities per agent.
const MaxCapabilities = 100

// MaxLabels is the maximum number of labels per agent.
const MaxLabels = 50

// MaxLabelKeyLen is the maximum label key length.
const MaxLabelKeyLen = 64

// MaxLabelValueLen is the maximum label value length.
const MaxLabelValueLen = 256

// ViolationType classifies the kind of validation failure.
type ViolationType string

const (
	ViolationRequired    ViolationType = "required"
	ViolationFormat      ViolationType = "format"
	ViolationRange       ViolationType = "range"
	ViolationSize        ViolationType = "size"
	ViolationEnum        ViolationType = "enum"
	ViolationMaxItems    ViolationType = "max_items"
	ViolationMaxLength   ViolationType = "max_length"
	ViolationUnrecognized ViolationType = "unrecognized"
)

// ValidationViolation describes a single validation error.
type ValidationViolation struct {
	Field     string        // dot-separated field path
	Type      ViolationType // category of violation
	Message   string        // human-readable description
}

func (v ValidationViolation) Error() string {
	return fmt.Sprintf("%s: %s (%s)", v.Field, v.Message, v.Type)
}

// ValidationError holds all violations found during manifest validation.
type ValidationError struct {
	Violations []ValidationViolation
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		msgs[i] = v.Error()
	}
	return fmt.Sprintf("manifest validation failed: %s", strings.Join(msgs, "; "))
}

// nameRegexp matches lowercase alphanumeric + hyphens, 1-253 chars.
var nameRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,252}$`)

// semverRegexp matches MAJOR.MINOR.PATCH semantic versioning.
var semverRegexp = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// validRuntimes is the set of allowed runtime type values.
var validRuntimes = map[RuntimeType]bool{
	RuntimeClaude:  true,
	RuntimeKiro:    true,
	RuntimeBedrock: true,
	RuntimeGemini:  true,
	RuntimeCustom:  true,
}

// rawManifest is the intermediate YAML deserialization target.
// It mirrors the YAML schema from the design doc.
type rawManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Version   string            `yaml:"version"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Runtime      string `yaml:"runtime"`
		Capabilities []struct {
			Name string `yaml:"name"`
			Type string `yaml:"type"`
		} `yaml:"capabilities"`
		SLO struct {
			Latency  *int     `yaml:"latency"`
			Accuracy *float64 `yaml:"accuracy"`
			Cost     *float64 `yaml:"cost"`
		} `yaml:"slo"`
		Resources struct {
			MaxConcurrentMissions int `yaml:"maxConcurrentMissions"`
			MaxMemoryMB           int `yaml:"maxMemoryMB"`
		} `yaml:"resources"`
		Deployment struct {
			Strategy      string `yaml:"strategy"`
			CanaryPercent int    `yaml:"canaryPercent"`
			MaxInstances  int    `yaml:"maxInstances"`
			DrainTimeout  string `yaml:"drainTimeout"`
		} `yaml:"deployment"`
	} `yaml:"spec"`
}

// ParseAndValidateManifest parses raw YAML bytes into an AgentManifest
// and validates all fields. Returns all violations found.
func ParseAndValidateManifest(data []byte) (*AgentManifest, error) {
	// Check size constraint first.
	if len(data) > MaxManifestSize {
		return nil, &ValidationError{
			Violations: []ValidationViolation{{
				Field:   "",
				Type:    ViolationSize,
				Message: fmt.Sprintf("manifest size %d bytes exceeds maximum %d bytes", len(data), MaxManifestSize),
			}},
		}
	}

	var raw rawManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, &ValidationError{
			Violations: []ValidationViolation{{
				Field:   "",
				Type:    ViolationFormat,
				Message: fmt.Sprintf("YAML parse error: %v", err),
			}},
		}
	}

	violations := validateRawManifest(&raw)
	if len(violations) > 0 {
		return nil, &ValidationError{Violations: violations}
	}

	// Convert to domain type.
	manifest := convertRawToManifest(&raw)
	return manifest, nil
}

// validateRawManifest checks all validation rules and collects violations.
func validateRawManifest(raw *rawManifest) []ValidationViolation {
	var violations []ValidationViolation

	// Required field: name
	if raw.Metadata.Name == "" {
		violations = append(violations, ValidationViolation{
			Field:   "metadata.name",
			Type:    ViolationRequired,
			Message: "name is required",
		})
	} else if !nameRegexp.MatchString(raw.Metadata.Name) {
		violations = append(violations, ValidationViolation{
			Field:   "metadata.name",
			Type:    ViolationFormat,
			Message: "name must be 1-253 lowercase alphanumeric characters or hyphens, starting with alphanumeric",
		})
	}

	// Required field: version
	if raw.Metadata.Version == "" {
		violations = append(violations, ValidationViolation{
			Field:   "metadata.version",
			Type:    ViolationRequired,
			Message: "version is required",
		})
	} else if !semverRegexp.MatchString(raw.Metadata.Version) {
		violations = append(violations, ValidationViolation{
			Field:   "metadata.version",
			Type:    ViolationFormat,
			Message: "version must be semantic versioning format MAJOR.MINOR.PATCH",
		})
	}

	// Required field: runtime
	if raw.Spec.Runtime == "" {
		violations = append(violations, ValidationViolation{
			Field:   "spec.runtime",
			Type:    ViolationRequired,
			Message: "runtime is required",
		})
	} else if !validRuntimes[RuntimeType(raw.Spec.Runtime)] {
		violations = append(violations, ValidationViolation{
			Field:   "spec.runtime",
			Type:    ViolationEnum,
			Message: fmt.Sprintf("runtime must be one of: claude, kiro, bedrock, gemini, custom; got %q", raw.Spec.Runtime),
		})
	}

	// Required field: capabilities (at least one)
	if len(raw.Spec.Capabilities) == 0 {
		violations = append(violations, ValidationViolation{
			Field:   "spec.capabilities",
			Type:    ViolationRequired,
			Message: "at least one capability is required",
		})
	} else if len(raw.Spec.Capabilities) > MaxCapabilities {
		violations = append(violations, ValidationViolation{
			Field:   "spec.capabilities",
			Type:    ViolationMaxItems,
			Message: fmt.Sprintf("capabilities count %d exceeds maximum %d", len(raw.Spec.Capabilities), MaxCapabilities),
		})
	}

	// Validate individual capabilities have names
	for i, cap := range raw.Spec.Capabilities {
		if cap.Name == "" {
			violations = append(violations, ValidationViolation{
				Field:   fmt.Sprintf("spec.capabilities[%d].name", i),
				Type:    ViolationRequired,
				Message: "capability name is required",
			})
		}
	}

	// Validate labels
	if len(raw.Metadata.Labels) > MaxLabels {
		violations = append(violations, ValidationViolation{
			Field:   "metadata.labels",
			Type:    ViolationMaxItems,
			Message: fmt.Sprintf("labels count %d exceeds maximum %d", len(raw.Metadata.Labels), MaxLabels),
		})
	}
	for key, value := range raw.Metadata.Labels {
		if len(key) > MaxLabelKeyLen {
			violations = append(violations, ValidationViolation{
				Field:   fmt.Sprintf("metadata.labels[%s]", key),
				Type:    ViolationMaxLength,
				Message: fmt.Sprintf("label key length %d exceeds maximum %d", len(key), MaxLabelKeyLen),
			})
		}
		if len(value) > MaxLabelValueLen {
			violations = append(violations, ValidationViolation{
				Field:   fmt.Sprintf("metadata.labels[%s]", key),
				Type:    ViolationMaxLength,
				Message: fmt.Sprintf("label value length %d exceeds maximum %d", len(value), MaxLabelValueLen),
			})
		}
	}

	// Validate SLO bounds
	violations = append(violations, validateSLO(raw)...)

	// Validate resources (required)
	if raw.Spec.Resources.MaxConcurrentMissions == 0 && raw.Spec.Resources.MaxMemoryMB == 0 {
		violations = append(violations, ValidationViolation{
			Field:   "spec.resources",
			Type:    ViolationRequired,
			Message: "resources must specify at least maxConcurrentMissions or maxMemoryMB",
		})
	}

	// Validate deployment (required)
	if raw.Spec.Deployment.Strategy == "" {
		violations = append(violations, ValidationViolation{
			Field:   "spec.deployment.strategy",
			Type:    ViolationRequired,
			Message: "deployment strategy is required",
		})
	}

	return violations
}

// validateSLO checks SLO field bounds.
func validateSLO(raw *rawManifest) []ValidationViolation {
	var violations []ValidationViolation

	// SLO section itself is required (at least one bound)
	hasSLO := raw.Spec.SLO.Latency != nil || raw.Spec.SLO.Accuracy != nil || raw.Spec.SLO.Cost != nil
	if !hasSLO {
		violations = append(violations, ValidationViolation{
			Field:   "spec.slo",
			Type:    ViolationRequired,
			Message: "at least one SLO bound (latency, accuracy, or cost) is required",
		})
		return violations
	}

	if raw.Spec.SLO.Latency != nil {
		lat := *raw.Spec.SLO.Latency
		if lat < 1 || lat > 60000 {
			violations = append(violations, ValidationViolation{
				Field:   "spec.slo.latency",
				Type:    ViolationRange,
				Message: fmt.Sprintf("latency %d must be in range [1, 60000] ms", lat),
			})
		}
	}

	if raw.Spec.SLO.Accuracy != nil {
		acc := *raw.Spec.SLO.Accuracy
		if acc < 0.0 || acc > 100.0 {
			violations = append(violations, ValidationViolation{
				Field:   "spec.slo.accuracy",
				Type:    ViolationRange,
				Message: fmt.Sprintf("accuracy %.2f must be in range [0.0, 100.0]%%", acc),
			})
		}
	}

	if raw.Spec.SLO.Cost != nil {
		cost := *raw.Spec.SLO.Cost
		if cost < 0 {
			violations = append(violations, ValidationViolation{
				Field:   "spec.slo.cost",
				Type:    ViolationRange,
				Message: fmt.Sprintf("cost %.4f must be non-negative", cost),
			})
		}
	}

	return violations
}

// convertRawToManifest converts the raw YAML representation to domain type.
func convertRawToManifest(raw *rawManifest) *AgentManifest {
	caps := make([]Capability, len(raw.Spec.Capabilities))
	for i, c := range raw.Spec.Capabilities {
		caps[i] = Capability{Name: c.Name, Type: c.Type}
	}

	var slo SLODefinition
	if raw.Spec.SLO.Latency != nil {
		slo.Latency = &LatencyBound{MaxMs: *raw.Spec.SLO.Latency}
	}
	if raw.Spec.SLO.Accuracy != nil {
		slo.Accuracy = &AccuracyBound{MinPercent: *raw.Spec.SLO.Accuracy}
	}
	if raw.Spec.SLO.Cost != nil {
		slo.Cost = &CostBound{MaxCost: *raw.Spec.SLO.Cost}
	}

	var drainTimeout time.Duration
	if raw.Spec.Deployment.DrainTimeout != "" {
		drainTimeout, _ = time.ParseDuration(raw.Spec.Deployment.DrainTimeout)
	}

	return &AgentManifest{
		Name:        raw.Metadata.Name,
		Namespace:   raw.Metadata.Namespace,
		Version:     raw.Metadata.Version,
		Labels:      raw.Metadata.Labels,
		RuntimeType: RuntimeType(raw.Spec.Runtime),
		Capabilities: caps,
		SLOs:        slo,
		Resources: ResourceLimits{
			MaxConcurrentMissions: raw.Spec.Resources.MaxConcurrentMissions,
			MaxMemoryMB:           raw.Spec.Resources.MaxMemoryMB,
		},
		Deployment: DeploymentStrategy{
			Type:          raw.Spec.Deployment.Strategy,
			CanaryPercent: raw.Spec.Deployment.CanaryPercent,
			MaxInstances:  raw.Spec.Deployment.MaxInstances,
			DrainTimeout:  drainTimeout,
		},
	}
}
