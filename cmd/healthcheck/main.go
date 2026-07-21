package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/luckymaomi/llmgateway/internal/buildinfo"
)

func main() {
	showVersion := flag.Bool("version", false, "print build identity and exit")
	target := flag.String("url", "http://127.0.0.1:8080/health/ready", "health endpoint URL")
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stdout, buildinfo.JSON())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, *target, nil)
	if err != nil {
		fatal(err)
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode))
	}
}

func fatal(err error) {
	if !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}
