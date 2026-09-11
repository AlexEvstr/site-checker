package main

import (
	"net/http"
	"time"
)

type CheckResult struct {
	URL        string
	StatusCode int
	Duration   time.Duration
	Err        error
}

func checkURL(url string) CheckResult {
	start := time.Now()
	client := http.Client{
		Timeout: 5 * time.Second,
	}
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
