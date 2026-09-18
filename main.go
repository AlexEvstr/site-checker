package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
)

var ErrInvalidWorkers = errors.New("workers must be greater than zero")
var ErrInvalidTimeout = errors.New("timeout must be greater than zero")

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

func isHealthy(result CheckResult) bool {
	return result.Err == nil &&
		result.StatusCode >= http.StatusOK &&
		result.StatusCode < http.StatusBadRequest
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

func checkURLs(
	ctx context.Context,
	client *http.Client,
	urls []string,
	workers int,
) ([]CheckResult, error) {
	if err := validateWorkers(workers); err != nil {
		return nil, err
	}

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

	return final, nil
}

func validateWorkers(workers int) error {
	if workers <= 0 {
		return ErrInvalidWorkers
	}
	return nil
}

func run(
	ctx context.Context,
	args []string,
	client *http.Client,
	output io.Writer,
) (exitCode int, err error) {
	flags := flag.NewFlagSet("site-checker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	workers := flags.Int("workers", 3, "number of concurrent workers")
	timeout := flags.Duration(
		"timeout",
		5*time.Second,
		"HTTP client timeout",
	)
	if err := flags.Parse(args); err != nil {
		return 2, err
	}

	if err := validateWorkers(*workers); err != nil {
		return 2, err
	}

	if *timeout <= 0 {
		return 2, ErrInvalidTimeout
	}

	urls := flags.Args()
	if len(urls) == 0 {
		return 2, errors.New("at least one URL is required")
	}

	configuredClient := *client
	configuredClient.Timeout = *timeout

	results, err := checkURLs(ctx, &configuredClient, urls, *workers)
	if err != nil {
		return 2, err
	}

	healthy := 0

	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(
				output,
				"Error checking %s: %v in %v\n",
				result.URL,
				result.Err,
				result.Duration,
			)
		} else {
			if isHealthy(result) {
				healthy++
			}
			fmt.Fprintf(
				output,
				"Checked %s: %d in %v\n",
				result.URL,
				result.StatusCode,
				result.Duration,
			)
		}
	}
	failed := len(results) - healthy

	fmt.Fprintf(
		output,
		"Checked: %d, healthy: %d, failed: %d\n",
		len(results),
		healthy,
		failed,
	)

	if failed > 0 {
		return 1, nil
	}

	return 0, nil
}

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
	)

	client := &http.Client{}

	exitCode, err := run(
		ctx,
		os.Args[1:],
		client,
		os.Stdout,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	stop()
	os.Exit(exitCode)
}
