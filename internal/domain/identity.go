package domain

// Role represents a user's authorization level.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
	RoleTeamLead Role = "team_lead"
)

// Identity represents an authenticated user or service.
type Identity struct {
	Subject string
	Team    string
	Scopes  []string
	Role    Role
}
