package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
)

type job struct {
	index int
	url   string
}

type indexedResult struct {
	index  int
	result CheckResult
}

type CheckResult struct {
	URL        string
	StatusCode int
	Duration   time.Duration
	Err        error
}

func worker(ctx context.Context, client *http.Client, jobs <-chan job, results chan<- indexedResult, wg *sync.WaitGroup) {
	defer wg.Done()
	for currentJob := range jobs {
		result := indexedResult{
			index:  currentJob.index,
			result: checkURL(ctx, client, currentJob.url),
		}
		results <- result
	}
}

func checkURL(ctx context.Context, client *http.Client, url string) CheckResult {
	start := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CheckResult{
			URL:      url,
			Duration: time.Since(start),
			Err:      err,
		}
	}
	resp, err := client.Do(request)
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

func checkURLs(ctx context.Context, client *http.Client, urls []string, workers int) []CheckResult {
	jobs := make(chan job, len(urls))
	results := make(chan indexedResult)
	final := make([]CheckResult, len(urls))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker(ctx, client, jobs, results, &wg)
	}

	for index, url := range urls {
		jobs <- job{index: index, url: url}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		final[res.index] = res.result
	}

	return final
}

func validateWorkers(workers int) error {
	if workers <= 0 {
		return fmt.Errorf("number of workers must be greater than 0")
	}
	return nil
}

func main() {
	workers := flag.Int("workers", 3, "number of concurrent workers")
	flag.Parse()
	urls := flag.Args()
	if err := validateWorkers(*workers); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "usage: site-checker [-workers N] <url> [url...]")
		os.Exit(1)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
	)
	defer stop()

	results := checkURLs(ctx, client, urls, *workers)

	for _, result := range results {
		if result.Err != nil {
			fmt.Printf("Error checking %s: %v in %v\n", result.URL, result.Err, result.Duration)
		} else {
			fmt.Printf("Checked %s: %d in %v\n", result.URL, result.StatusCode, result.Duration)
		}
	}
}
