package assistant

import (
	"encoding/json"
	"net/http"
	"sonara/backend/internal/usecases/assistant"
	"strings"
)

func NewRespondHandler(uc assistant.RespondUsecase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input assistant.RespondInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil || strings.TrimSpace(input.Message) == "" {
			writeError(w, http.StatusBadRequest, "message is required")
			return
		}
		output, err := uc.Execute(r.Context(), input)
		if err != nil {
			writeError(w, http.StatusBadGateway, "assistant request failed")
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
