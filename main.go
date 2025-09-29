package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

var (
	apiKey        = os.Getenv("OPENAI_API_KEY")
	apiOrg        = os.Getenv("OPENAI_ORGANIZATION")
	apiProject    = os.Getenv("OPENAI_PROJECT")
	serviceAPIKey = os.Getenv("SERVICE_API_KEY")
)

// Estruturas
type GenerateRequest struct {
	DeviceName string `json:"device_name"`
	Keyword    string `json:"keyword"`
	Language   string `json:"language"`
	Prompt     string `json:"prompt"`
	Refresh    bool   `json:"refresh"`
}

type CodeResponse struct {
	DeviceName string `json:"device_name"`
	Keyword    string `json:"keyword"`
	Language   string `json:"language"`
	Prompt     string `json:"prompt"`
	Output     string `json:"output"`
}

type OpenAIRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type OpenAIResponse struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// Banco em memória
var (
	mu    sync.RWMutex
	store = make(map[string]CodeResponse)
)

func main() {
	if serviceAPIKey == "" {
		log.Fatal("Variável de ambiente 'SERVICE_API_KEY' não definida.")
	}

	http.Handle("/generate", authMiddleware(http.HandlerFunc(generateHandler)))
	http.Handle("/code", authMiddleware(http.HandlerFunc(codeHandler)))

	fmt.Println("Servidor rodando em http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Middleware de autenticação
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key != serviceAPIKey {
			http.Error(w, "Chave de API inválida", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// 1️⃣ /generate → gera/atualiza código
func generateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	var req GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	if req.DeviceName == "" || req.Keyword == "" || req.Language == "" || req.Prompt == "" {
		http.Error(w, "device_name, keyword, language e prompt são obrigatórios", http.StatusBadRequest)
		return
	}

	mu.RLock()
	entry, exists := store[req.DeviceName]
	mu.RUnlock()

	if !exists || req.Refresh {
		fmt.Println("Chamando API ChatGPT...")
		output, err := callOpenAI(req.Prompt)
		if err != nil {
			http.Error(w, "Erro OpenAI: "+err.Error(), http.StatusInternalServerError)
			return
		}

		entry = CodeResponse{
			DeviceName: req.DeviceName,
			Keyword:    req.Keyword,
			Language:   req.Language,
			Prompt:     req.Prompt,
			Output:     output,
		}

		mu.Lock()
		store[req.DeviceName] = entry
		mu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

// 2️⃣ /code → busca código salvo
func codeHandler(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Query().Get("device")
	if device == "" {
		http.Error(w, "device é obrigatório", http.StatusBadRequest)
		return
	}

	mu.RLock()
	entry, exists := store[device]
	mu.RUnlock()

	if !exists {
		http.Error(w, "device não encontrado", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

// --- Integração com OpenAI ---
func callOpenAI(prompt string) (string, error) {
	reqBody := OpenAIRequest{Model: "o4-mini", Input: prompt}
	bodyBytes, _ := json.Marshal(reqBody)

	client := &http.Client{}
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	if apiOrg != "" {
		req.Header.Set("OpenAI-Organization", apiOrg)
	}
	if apiProject != "" {
		req.Header.Set("OpenAI-Project", apiProject)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	var oaResp OpenAIResponse
	if err := json.Unmarshal(b, &oaResp); err == nil {
		if len(oaResp.Output) > 0 && len(oaResp.Output[0].Content) > 0 {
			return strings.TrimSpace(oaResp.Output[0].Content[0].Text), nil
		}
	}

	return string(b), nil
}
