package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentplane/agentplane/internal/domain"
	"github.com/agentplane/agentplane/internal/store"
	"github.com/google/uuid"
)

// MaxVersionHistory is the maximum number of version records kept per agent.
const MaxVersionHistory = 50

// ErrDuplicateAgent is returned when name+version already exists.
var ErrDuplicateAgent = errors.New("agent with this name and version already exists")

// ErrAgentNotFound is returned when the requested agent does not exist.
var ErrAgentNotFound = errors.New("agent not found")

// nameRegexp matches lowercase alphanumeric + hyphens, 1-253 chars.
var nameRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,252}$`)

// semverRegexp matches MAJOR.MINOR.PATCH semantic versioning.
var semverRegexp = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// validRuntimes is the set of allowed runtime type values.
var validRuntimes = map[domain.RuntimeType]bool{
	domain.RuntimeClaude:  true,
	domain.RuntimeKiro:    true,
	domain.RuntimeBedrock: true,
	domain.RuntimeCustom:  true,
}

// Registry implements AgentRegistryService.
type Registry struct {
	store store.Store
}

// New creates a new Registry backed by the given store.
func New(s store.Store) *Registry {
	return &Registry{store: s}
}

// Register validates and stores a new agent entry.
func (r *Registry) Register(ctx context.Context, manifest domain.AgentManifest) (*domain.AgentEntry, error) {
	// Validate manifest fields directly (manifest is already parsed, not raw YAML).
	if err := validateManifestFields(manifest); err != nil {
		return nil, err
	}

	// Check for duplicate name+version.
	existing, err := r.store.Agents().GetByName(ctx, manifest.Name, manifest.Namespace)
	if err == nil && existing != nil && existing.Version == manifest.Version {
		return nil, ErrDuplicateAgent
	}

	// Generate unique ID.
	id := uuid.New().String()
	now := time.Now().UTC()

	entry := &domain.AgentEntry{
		ID:           id,
		Name:         manifest.Name,
		Namespace:    manifest.Namespace,
		Version:      manifest.Version,
		RuntimeType:  manifest.RuntimeType,
		Capabilities: manifest.Capabilities,
		Labels:       manifest.Labels,
		SLOs:         manifest.SLOs,
		Resources:    manifest.Resources,
		Deployment:   manifest.Deployment,
		Status:       domain.AgentStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := r.store.Agents().Create(ctx, entry); err != nil {
		return nil, fmt.Errorf("storing agent entry: %w", err)
	}

	// Append version history.
	version := &domain.AgentVersion{
		ID:           uuid.New().String(),
		AgentID:      id,
		Version:      manifest.Version,
		RegisteredAt: now,
	}
	if err := r.store.Agents().AddVersion(ctx, version); err != nil {
		return nil, fmt.Errorf("adding version history: %w", err)
	}

	// Enforce version history cap.
	if err := r.trimVersionHistory(ctx, id); err != nil {
		return nil, fmt.Errorf("trimming version history: %w", err)
	}

	return entry, nil
}

// Get retrieves an agent by ID.
func (r *Registry) Get(ctx context.Context, id string) (*domain.AgentEntry, error) {
	entry, err := r.store.Agents().Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if entry == nil {
		return nil, ErrAgentNotFound
	}
	return entry, nil
}

// List returns agents matching filter criteria.
func (r *Registry) List(ctx context.Context, filter domain.AgentFilter) ([]*domain.AgentEntry, error) {
	return r.store.Agents().List(ctx, filter)
}

// FindByCapability returns agents declaring a specific capability.
func (r *Registry) FindByCapability(ctx context.Context, capability string) ([]*domain.AgentEntry, error) {
	if capability == "" {
		return nil, errors.New("capability must not be empty")
	}
	return r.store.Agents().QueryByCapability(ctx, capability)
}

// Deregister removes an agent from the registry.
func (r *Registry) Deregister(ctx context.Context, id string) error {
	// Verify agent exists.
	entry, err := r.store.Agents().Get(ctx, id)
	if err != nil {
		return fmt.Errorf("deregister lookup: %w", err)
	}
	if entry == nil {
		return ErrAgentNotFound
	}
	return r.store.Agents().Delete(ctx, id)
}

// trimVersionHistory enforces the 50-entry cap on version history.
func (r *Registry) trimVersionHistory(ctx context.Context, agentID string) error {
	versions, err := r.store.Agents().ListVersions(ctx, agentID)
	if err != nil {
		return err
	}

	if len(versions) <= MaxVersionHistory {
		return nil
	}

	// Versions are ordered by registered_at DESC (newest first).
	// Remove the oldest entries beyond the cap (at the tail of the slice).
	for i := MaxVersionHistory; i < len(versions); i++ {
		if err := r.store.Agents().DeleteVersion(ctx, versions[i].ID); err != nil {
			return err
		}
	}
	return nil
}

// validateManifestFields validates a parsed AgentManifest's fields.
func validateManifestFields(m domain.AgentManifest) error {
	var violations []domain.ValidationViolation

	// Name: required, 1-253 lowercase alphanumeric + hyphens.
	if m.Name == "" {
		violations = append(violations, domain.ValidationViolation{
			Field:   "name",
			Type:    domain.ViolationRequired,
			Message: "name is required",
		})
	} else if !nameRegexp.MatchString(m.Name) {
		violations = append(violations, domain.ValidationViolation{
			Field:   "name",
			Type:    domain.ViolationFormat,
			Message: "name must be 1-253 lowercase alphanumeric characters or hyphens, starting with alphanumeric",
		})
	}

	// Version: required, semver format.
	if m.Version == "" {
		violations = append(violations, domain.ValidationViolation{
			Field:   "version",
			Type:    domain.ViolationRequired,
			Message: "version is required",
		})
	} else if !semverRegexp.MatchString(m.Version) {
		violations = append(violations, domain.ValidationViolation{
			Field:   "version",
			Type:    domain.ViolationFormat,
			Message: "version must be semantic versioning format MAJOR.MINOR.PATCH",
		})
	}

	// Runtime: required, must be valid enum.
	if m.RuntimeType == "" {
		violations = append(violations, domain.ValidationViolation{
			Field:   "runtime",
			Type:    domain.ViolationRequired,
			Message: "runtime is required",
		})
	} else if !validRuntimes[m.RuntimeType] {
		violations = append(violations, domain.ValidationViolation{
			Field:   "runtime",
			Type:    domain.ViolationEnum,
			Message: fmt.Sprintf("runtime must be one of: claude, kiro, bedrock, custom; got %q", m.RuntimeType),
		})
	}

	// Capabilities: at least one, max 100.
	if len(m.Capabilities) == 0 {
		violations = append(violations, domain.ValidationViolation{
			Field:   "capabilities",
			Type:    domain.ViolationRequired,
			Message: "at least one capability is required",
		})
	} else if len(m.Capabilities) > domain.MaxCapabilities {
		violations = append(violations, domain.ValidationViolation{
			Field:   "capabilities",
			Type:    domain.ViolationMaxItems,
			Message: fmt.Sprintf("capabilities count %d exceeds maximum %d", len(m.Capabilities), domain.MaxCapabilities),
		})
	}

	// Labels: max 50 keys, key max 64, value max 256.
	if len(m.Labels) > domain.MaxLabels {
		violations = append(violations, domain.ValidationViolation{
			Field:   "labels",
			Type:    domain.ViolationMaxItems,
			Message: fmt.Sprintf("labels count %d exceeds maximum %d", len(m.Labels), domain.MaxLabels),
		})
	}
	for key, value := range m.Labels {
		if len(key) > domain.MaxLabelKeyLen {
			violations = append(violations, domain.ValidationViolation{
				Field:   fmt.Sprintf("labels[%s]", key),
				Type:    domain.ViolationMaxLength,
				Message: fmt.Sprintf("label key length %d exceeds maximum %d", len(key), domain.MaxLabelKeyLen),
			})
		}
		if len(value) > domain.MaxLabelValueLen {
			violations = append(violations, domain.ValidationViolation{
				Field:   fmt.Sprintf("labels[%s]", key),
				Type:    domain.ViolationMaxLength,
				Message: fmt.Sprintf("label value length %d exceeds maximum %d", len(value), domain.MaxLabelValueLen),
			})
		}
	}

	if len(violations) > 0 {
		return &domain.ValidationError{Violations: violations}
	}
	return nil
}
