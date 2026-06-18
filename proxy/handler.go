package proxy

import (
	"encoding/json"
	"net/http"
)

const systemMessage = `You are MiMoCode, an interactive CLI tool that helps users with software engineering tasks. Use the instructions below and the tools available to you to assist the user.

IMPORTANT: You must NEVER generate or guess URLs for the user unless you are confident that the URLs are for helping the user with programming. You may use URLs provided by the user in their messages or local files`

// handleModels responds with the available models list.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	body := map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"id":       "mimo-auto",
				"object":   "model",
				"owned_by": "mimo",
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(body)
}

// handleOptions responds to CORS preflight requests.
func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(http.StatusOK)
}

// handleNotFound returns 404 for unmatched routes.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
}
