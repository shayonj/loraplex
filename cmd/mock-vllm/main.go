package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

func main() {
	listen := flag.String("listen", ":8000", "listen address")
	model := flag.String("model", "mock-llama-8b", "model name")
	latency := flag.Duration("latency", 10*time.Millisecond, "simulated inference latency")
	flag.Parse()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	http.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": *model, "object": "model", "created": time.Now().Unix(), "owned_by": "vllm"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)

		time.Sleep(*latency + time.Duration(rand.Intn(10))*time.Millisecond)

		reqModel, _ := req["model"].(string)
		if reqModel == "" {
			reqModel = *model
		}

		stream, _ := req["stream"].(bool)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(200)

			flusher, _ := w.(http.Flusher)
			chunk := map[string]any{
				"id": fmt.Sprintf("chatcmpl-%d", rand.Int63()),
				"object": "chat.completion.chunk",
				"model":  reqModel,
				"choices": []map[string]any{
					{"index": 0, "delta": map[string]string{"role": "assistant", "content": "Hello"}, "finish_reason": nil},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			if flusher != nil {
				flusher.Flush()
			}

			done := map[string]any{
				"id": fmt.Sprintf("chatcmpl-%d", rand.Int63()),
				"object": "chat.completion.chunk",
				"model":  reqModel,
				"choices": []map[string]any{
					{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"},
				},
			}
			doneData, _ := json.Marshal(done)
			fmt.Fprintf(w, "data: %s\n\n", doneData)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}

		resp := map[string]any{
			"id":      fmt.Sprintf("chatcmpl-%d", rand.Int63()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   reqModel,
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": "Hello! I'm a mock vLLM response."},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	fmt.Printf("mock-vllm listening on %s (model=%s latency=%s)\n", *listen, *model, *latency)
	http.ListenAndServe(*listen, nil)
}
