package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type result struct {
	Status      int
	Completed   bool
	Interrupted bool
}

type report struct {
	Requests    int `json:"requests"`
	StatusOK    int `json:"statusOk"`
	Completed   int `json:"completed"`
	Interrupted int `json:"interrupted"`
}

func main() {
	baseURL := flag.String("base-url", "", "isolated Gateway instance to terminate")
	runID := flag.String("run-id", "", "capacity fixture run identifier")
	model := flag.String("model", "capacity-chat", "published model alias")
	requestCount := flag.Int("requests", 128, "held streams")
	flag.Parse()
	if !strings.HasPrefix(*baseURL, "http://127.0.0.1:") || len(*runID) != 8 || *requestCount < 1 || *requestCount > 200 {
		fmt.Fprintln(os.Stderr, "recovery acceptance arguments are invalid")
		os.Exit(2)
	}
	transport := &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 256, MaxConnsPerHost: 256, ResponseHeaderTimeout: 30 * time.Second}
	client := &http.Client{Transport: transport}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results := make(chan result, *requestCount)
	var group sync.WaitGroup
	for index := 0; index < *requestCount; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			results <- execute(ctx, client, *baseURL, *runID, *model, index)
		}(index)
	}
	group.Wait()
	close(results)
	report := report{Requests: *requestCount}
	for item := range results {
		if item.Status == http.StatusOK {
			report.StatusOK++
		}
		if item.Completed {
			report.Completed++
		}
		if item.Interrupted {
			report.Interrupted++
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
	if report.StatusOK != report.Requests || report.Completed != 0 || report.Interrupted != report.Requests {
		os.Exit(1)
	}
}

func execute(ctx context.Context, client *http.Client, baseURL, runID, model string, index int) result {
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": "hold stream capacity recovery"}},
		"max_tokens": 16, "stream": true,
	})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	request.Header.Set("Authorization", fmt.Sprintf("Bearer llmg_capacity_%s_%03d", runID, index))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "recovery-first-"+uuid.NewString())
	response, err := client.Do(request)
	if err != nil {
		return result{Interrupted: true}
	}
	defer response.Body.Close()
	value := result{Status: response.StatusCode}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return value
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 8<<20))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "data: [DONE]" {
			value.Completed = true
		}
	}
	value.Interrupted = scanner.Err() != nil || !value.Completed
	return value
}
