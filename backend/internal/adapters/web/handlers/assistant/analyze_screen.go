package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"sonara/backend/internal/usecases/assistant"
)

func NewAnalyzeScreenHandler(uc assistant.AnalyzeScreenUsecase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input assistant.AnalyzeScreenInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20)).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		output, err := uc.Execute(r.Context(), input)
		if err != nil {
			if errors.Is(err, assistant.ErrInvalidImage) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusBadGateway, "screen analysis failed")
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}
