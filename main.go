package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

type CheckResult struct {
	URL        string
	StatusCode int
	Duration   time.Duration
	Err        error
}

func checkURL(client *http.Client, url string) CheckResult {
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return CheckResult{
			URL:      url,
			Duration: time.Since(start),
			Err:      err,
		}
	}
	defer resp.Body.Close()
	return CheckResult{
		URL:        url,
		StatusCode: resp.StatusCode,
		Duration:   time.Since(start),
	}
}

func main() {
	urls := os.Args[1:]
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "usage: site-checker <url> [url...]")
		os.Exit(1)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, url := range urls {
		result := checkURL(client, url)
		if result.Err != nil {
			fmt.Printf("Error checking %s: %v in %v\n", result.URL, result.Err, result.Duration)
		} else {
			fmt.Printf("Checked %s: %d in %v\n", result.URL, result.StatusCode, result.Duration)
		}
	}
}
