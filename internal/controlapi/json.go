package controlapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type dataEnvelope struct {
	Data any `json:"data"`
}

func decodeJSON(_ http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func writeData(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(dataEnvelope{Data: value})
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, _ error) {
	writeProblem(w, r, problem{
		Status:  http.StatusBadRequest,
		Code:    "invalid_request",
		Message: "Request body is invalid.",
	})
}
