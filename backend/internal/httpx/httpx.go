package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: code, Message: message})
}

func Decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

type StatusError struct {
	Code    int
	ErrCode string
	Message string
	Err     error
}

func (e *StatusError) Error() string { return e.Message }
func (e *StatusError) Unwrap() error { return e.Err }

func NewStatusError(status int, code, message string, err error) *StatusError {
	return &StatusError{Code: status, ErrCode: code, Message: message, Err: err}
}

func WriteErr(w http.ResponseWriter, err error) {
	var se *StatusError
	if errors.As(err, &se) {
		WriteError(w, se.Code, se.ErrCode, se.Message)
		return
	}
	WriteError(w, http.StatusInternalServerError, "internal", "internal server error")
}
