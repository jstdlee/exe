package server

import (
	"net/http"

	"exe/internal/jx/tools"
)

// VM Tools catalog (internal/jx/tools): the desktop's VM window lists it
// and opens each entry's command in a VM terminal window.
func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/jx/tools", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"categories": tools.Categories, "tools": tools.Catalog()})
		})
	})
}
