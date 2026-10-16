package assistant

import (
	"io"
	"net/http"
	"sonara/backend/internal/usecases/assistant"
)

func NewTranscribeHandler(uc assistant.TranscribeUsecase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
		if err := r.ParseMultipartForm(25 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "audio file is required; maximum size is 25 MB")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "audio file is required")
			return
		}
		defer file.Close()
		audio, err := io.ReadAll(io.LimitReader(file, (25<<20)+1))
		if err != nil || len(audio) == 0 || len(audio) > 25<<20 {
			writeError(w, http.StatusBadRequest, "audio file is empty or exceeds 25 MB")
			return
		}
		output, err := uc.Execute(r.Context(), assistant.TranscribeInput{Audio: audio, Filename: header.Filename, Language: r.FormValue("language")})
		if err != nil {
			writeError(w, http.StatusBadGateway, "transcription failed")
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}
