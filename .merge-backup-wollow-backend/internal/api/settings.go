package api

import (
	"encoding/json"
	"net/http"
)

type settingsResponse struct {
	AIProvider string `json:"aiProvider"`
	ModelName  string `json:"modelName"`
	BaseURL    string `json:"baseUrl"`
	HasAPIKey  bool   `json:"hasApiKey"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	var providerName, encryptedKey, model, baseURL string
	err := s.DB.QueryRow(`SELECT ai_provider, encrypted_api_key, model_name, base_url FROM settings WHERE id = 1`).
		Scan(&providerName, &encryptedKey, &model, &baseURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, settingsResponse{
		AIProvider: providerName,
		ModelName:  model,
		BaseURL:    baseURL,
		HasAPIKey:  encryptedKey != "",
	})
}

type putSettingsRequest struct {
	AIProvider string `json:"aiProvider"`
	ModelName  string `json:"modelName"`
	BaseURL    string `json:"baseUrl"`
	APIKey     string `json:"apiKey"` // empty string leaves existing key unchanged
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req putSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.AIProvider == "custom" && req.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "custom AI provider requires a base URL")
		return
	}

	if req.APIKey != "" {
		encryptedKey, err := s.Box.Encrypt(req.APIKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encrypt API key")
			return
		}
		_, err = s.DB.Exec(
			`UPDATE settings SET ai_provider = ?, model_name = ?, base_url = ?, encrypted_api_key = ? WHERE id = 1`,
			req.AIProvider, req.ModelName, req.BaseURL, encryptedKey,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
	} else {
		_, err := s.DB.Exec(
			`UPDATE settings SET ai_provider = ?, model_name = ?, base_url = ? WHERE id = 1`,
			req.AIProvider, req.ModelName, req.BaseURL,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
