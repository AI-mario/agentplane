// Package web provides embedded dashboard assets for the AgentPlane binary.
// The dashboard is a React SPA built into web/dashboard/dist/.
// If the dist directory is empty or missing at build time, an empty FS is embedded
// and the dashboard will return 404 (the API still functions normally).
package web

import (
	"embed"
	"io/fs"
)

//go:embed dashboard/dist
var dashboardFS embed.FS

// DashboardAssets returns an fs.FS rooted at the dashboard dist directory.
// This can be served directly via http.FileServer.
func DashboardAssets() (fs.FS, error) {
	return fs.Sub(dashboardFS, "dashboard/dist")
}
