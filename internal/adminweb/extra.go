package adminweb

// registerExtra adds the routes of later features (communication,
// printing, user tests); see F8.
func (s *Server) registerExtra(route func(pattern string, p perm, action string, h handler)) {}
