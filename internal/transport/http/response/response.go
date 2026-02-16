package response

import (
	"encoding/json"
	"net/http"
)

type Error struct {
	Error string `json:"error"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Err(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, Error{Error: msg})
}
