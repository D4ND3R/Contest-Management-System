package contestweb

import "net/http"

// registerExtra adds the communication, user test and printing pages
// (F8). It is a hook so that the core routes stay in one place.
func (s *Server) registerExtra(mux *http.ServeMux, auth func(func(http.ResponseWriter, *http.Request, *reqCtx)) http.HandlerFunc) {
}
