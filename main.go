package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var (
	apiKey     = os.Getenv("OPENAI_API_KEY")
	apiOrg     = os.Getenv("OPENAI_ORGANIZATION")
	apiProject = os.Getenv("OPENAI_PROJECT")

	serviceAPIKey = os.Getenv("SERVICE_API_KEY")
)

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

func main() {
	// Valida se a sua chave de API foi definida
	if serviceAPIKey == "" {
		log.Fatal("Variável de ambiente 'SERVICE_API_KEY' não definida.")
	}

	db, err := sql.Open("sqlite3", "./data.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	db.Exec(`CREATE TABLE IF NOT EXISTS entries (
       device_name TEXT PRIMARY KEY,
       keyword TEXT,
       language TEXT,
       prompt TEXT,
       output TEXT
    )`)

	// Protege os handlers com o middleware
	http.Handle("/generate", authMiddleware(http.HandlerFunc(generateHandler(db))))
	http.Handle("/code", authMiddleware(http.HandlerFunc(codeHandler(db))))

	fmt.Println("Servidor rodando em http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// authMiddleware é uma função que envolve seus handlers para adicionar autenticação
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pega o valor do cabeçalho 'X-API-Key'
		key := r.Header.Get("X-API-Key")

		// Se a chave não corresponder, retorna um erro de não autorizado
		if key != serviceAPIKey {
			http.Error(w, "Chave de API inválida", http.StatusUnauthorized)
			return
		}

		// Se a chave for válida, continua a execução para o próximo handler
		next.ServeHTTP(w, r)
	})
}

// 1️⃣ /generate → gera/atualiza código usando ChatGPT
func generateHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		var output string
		err := db.QueryRow("SELECT output FROM entries WHERE device_name = ?", req.DeviceName).Scan(&output)

		if err == sql.ErrNoRows || req.Refresh {
			fmt.Println("Chamando API ChatGPT...")
			output, err = callOpenAI(req.Prompt)
			if err != nil {
				http.Error(w, "Erro OpenAI: "+err.Error(), http.StatusInternalServerError)
				return
			}

			_, err = db.Exec(`
             INSERT INTO entries(device_name, keyword, language, prompt, output)
             VALUES(?, ?, ?, ?, ?)
             ON CONFLICT(device_name) DO UPDATE 
             SET keyword=excluded.keyword, language=excluded.language, prompt=excluded.prompt, output=excluded.output
          `, req.DeviceName, req.Keyword, req.Language, req.Prompt, output)
			if err != nil {
				http.Error(w, "Erro ao salvar no banco", http.StatusInternalServerError)
				return
			}
		}

		resp := CodeResponse{
			DeviceName: req.DeviceName,
			Keyword:    req.Keyword,
			Language:   req.Language,
			Prompt:     req.Prompt,
			Output:     output,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// 2️⃣ /code → busca código salvo para um device
func codeHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device := r.URL.Query().Get("device")
		if device == "" {
			http.Error(w, "device é obrigatório", http.StatusBadRequest)
			return
		}

		var resp CodeResponse
		err := db.QueryRow("SELECT device_name, keyword, language, prompt, output FROM entries WHERE device_name = ?", device).
			Scan(&resp.DeviceName, &resp.Keyword, &resp.Language, &resp.Prompt, &resp.Output)

		if err != nil {
			http.Error(w, "device não encontrado", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// --- Integração com ChatGPT ---
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
		if len(oaResp.Output) > 0 && len(oaResp.Output[1].Content) > 0 {
			return strings.TrimSpace(oaResp.Output[1].Content[0].Text), nil
		}
	}

	return string(b), nil
}
