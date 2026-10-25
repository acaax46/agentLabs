package platform

// Workspace is the space a deployment serves. There is exactly one per deployment today, so
// the workbench shows it but offers no switching; its identity is the tenant id.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Workspace describes the workspace the caller is working in.
func (s *Service) Workspace(actor Actor) Workspace {
	return Workspace{ID: actor.TenantID, Name: s.cfg.WorkspaceName}
}
