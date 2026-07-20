package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func echoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	json.NewEncoder(w).Encode(body)
}

func main() {
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/echo", echoHandler)
	fmt.Println("listening on :19876")
	http.ListenAndServe(":19876", nil)
}
