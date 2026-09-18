package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckURLSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	result := checkURL(context.Background(), client, server.URL)

	if result.Err != nil {
		t.Fatalf("expected no error, got %v", result.Err)
	}

	if result.StatusCode != http.StatusNoContent {
		t.Errorf(
			"expected status code %d, got %d",
			http.StatusNoContent,
			result.StatusCode,
		)
	}

	if result.URL != server.URL {
		t.Errorf("expected URL %q, got %q", server.URL, result.URL)
	}

	if result.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", result.Duration)
	}
}

func TestCheckURLUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := server.Client()
	client.Timeout = time.Second
	url := server.URL
	server.Close()

	result := checkURL(context.Background(), client, url)

	if result.Err == nil {
		t.Fatalf("expected an error, got nil")
	}

	if result.URL != url {
		t.Errorf("expected URL %q, got %q", url, result.URL)
	}

	if result.StatusCode != 0 {
		t.Errorf("expected status code 0, got %d", result.StatusCode)
	}

	if result.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", result.Duration)
	}
}

func TestCheckURLTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = 10 * time.Millisecond

	result := checkURL(context.Background(), client, server.URL)

	if result.Err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if result.StatusCode != 0 {
		t.Errorf("expected status code 0, got %d", result.StatusCode)
	}
}

func TestCheckURLsReturnsAllResultsInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			w.WriteHeader(http.StatusCreated)
		case "/second":
			w.WriteHeader(http.StatusAccepted)
		case "/third":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	urls := []string{
		server.URL + "/first",
		server.URL + "/second",
		server.URL + "/third",
	}

	results, err := checkURLs(
		context.Background(),
		server.Client(),
		urls,
		2,
	)
	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	if len(results) != len(urls) {
		t.Fatalf("expected %d results, got %d", len(urls), len(results))
	}

	expectedStatuses := []int{
		http.StatusCreated,
		http.StatusAccepted,
		http.StatusNoContent,
	}

	for i, result := range results {
		if result.URL != urls[i] {
			t.Errorf("expected URL %q at index %d, got %q", urls[i], i, result.URL)
		}

		if result.Err != nil {
			t.Fatalf("unexpected error at index %d: %v", i, result.Err)
		}
		if result.StatusCode != expectedStatuses[i] {
			t.Errorf(
				"expected status %d at index %d, got %d",
				expectedStatuses[i],
				i,
				result.StatusCode,
			)
		}
	}
}

func TestCheckURLsUnavailableDoesNotStopOthers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	unavailableServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	unavailableURL := unavailableServer.URL
	unavailableServer.Close()

	urls := []string{
		server.URL + "/first",
		unavailableURL,
		server.URL + "/second",
	}

	client := &http.Client{
		Timeout: time.Second,
	}
	results, err := checkURLs(context.Background(), client, urls, 2)
	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	if len(results) != len(urls) {
		t.Fatalf("expected %d results, got %d", len(urls), len(results))
	}

	if results[0].Err != nil {
		t.Errorf("unexpected error for first URL: %v", results[0].Err)
	}
	if results[0].StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for first URL, got %d", results[0].StatusCode)
	}

	if results[1].Err == nil {
		t.Errorf("expected error for unavailable server, got nil")
	}
	if results[2].Err != nil {
		t.Errorf("unexpected error for second URL: %v", results[2].Err)
	}
	if results[2].StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for second URL, got %d", results[2].StatusCode)
	}

	for i, result := range results {
		if result.URL != urls[i] {
			t.Errorf("expected URL %q at index %d, got %q", urls[i], i, result.URL)
		}
	}
}

func TestCheckURLsDoesNotExceedWorkerLimit(t *testing.T) {
	const workers = 3

	var active atomic.Int32
	var maxActive atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)

		for {
			previousMax := maxActive.Load()

			if current <= previousMax {
				break
			}

			if maxActive.CompareAndSwap(previousMax, current) {
				break
			}
		}

		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urls := make([]string, 12)

	for i := range urls {
		urls[i] = fmt.Sprintf("%s/%d", server.URL, i)
	}

	results, err := checkURLs(
		context.Background(),
		server.Client(),
		urls,
		workers,
	)
	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	maximum := maxActive.Load()

	if maximum <= 1 {
		t.Fatalf(
			"expected concurrent requests, max active requests: %d",
			maximum,
		)
	}

	if maximum > workers {
		t.Fatalf(
			"worker limit exceeded: limit %d, max active requests %d",
			workers,
			maximum,
		)
	}
	if len(results) != len(urls) {
		t.Fatalf("expected %d results, got %d", len(urls), len(results))
	}
}

func TestCheckURLsContextCancellationStopsRequest(t *testing.T) {
	started := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		results []CheckResult
		err     error
	}

	done := make(chan outcome, 1)

	go func() {
		results, err := checkURLs(
			ctx,
			server.Client(),
			[]string{server.URL},
			1,
		)
		done <- outcome{
			results: results,
			err:     err,
		}
	}()
	select {
	case <-started:
		// Запрос дошёл до handler.
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("unexpected configuration error: %v", result.err)
		}

		results := result.results
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Err == nil {
			t.Errorf("expected error due to context cancellation, got nil")
		}

		if !errors.Is(results[0].Err, context.Canceled) {
			t.Errorf(
				"expected context.Canceled, got %v",
				results[0].Err,
			)
		}
	case <-time.After(time.Second):
		t.Fatalf("test timed out waiting for context cancellation")
	}
}

func TestValidateWorkersRejectsNonPositiveValues(t *testing.T) {
	if err := validateWorkers(0); err == nil {
		t.Errorf("expected error for 0 workers, got nil")
	}
	if err := validateWorkers(-1); err == nil {
		t.Errorf("expected error for negative workers, got nil")
	}
	if err := validateWorkers(1); err != nil {
		t.Errorf("expected no error for positive workers, got %v", err)
	}
}

func TestCheckURLsRejectsNonPositiveWorkerCount(t *testing.T) {
	tests := []struct {
		name    string
		workers int
	}{
		{name: "zero", workers: 0},
		{name: "negative", workers: -1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, err := checkURLs(
				context.Background(),
				http.DefaultClient,
				[]string{"http://example.invalid"},
				test.workers,
			)

			if !errors.Is(err, ErrInvalidWorkers) {
				t.Fatalf(
					"expected ErrInvalidWorkers, got %v",
					err,
				)
			}

			if results != nil {
				t.Errorf("expected nil results, got %v", results)
			}
		})
	}
}
func TestRunRequiresURL(t *testing.T) {
	var output bytes.Buffer

	exitCode, err := run(
		context.Background(),
		nil,
		http.DefaultClient,
		&output,
	)

	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if err.Error() != "at least one URL is required" {
		t.Errorf("unexpected error: %v", err)
	}

	if exitCode != 2 {
		t.Errorf("expected exit code 2, got %d", exitCode)
	}

	if output.Len() != 0 {
		t.Errorf("expected no output, got %q", output.String())
	}
}
func TestRunRejectsNonPositiveWorkersAndTimeout(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectedErr error
	}{
		{
			name:        "zero workers",
			args:        []string{"-workers", "0", "http://example.invalid"},
			expectedErr: ErrInvalidWorkers,
		},
		{
			name:        "negative workers",
			args:        []string{"-workers", "-1", "http://example.invalid"},
			expectedErr: ErrInvalidWorkers,
		},
		{
			name:        "zero timeout",
			args:        []string{"-timeout", "0s", "http://example.invalid"},
			expectedErr: ErrInvalidTimeout,
		},
		{
			name:        "negative timeout",
			args:        []string{"-timeout", "-1s", "http://example.invalid"},
			expectedErr: ErrInvalidTimeout,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer

			exitCode, err := run(
				context.Background(),
				test.args,
				http.DefaultClient,
				&output,
			)

			if !errors.Is(err, test.expectedErr) {
				t.Fatalf(
					"expected error %v, got %v",
					test.expectedErr,
					err,
				)
			}

			if exitCode != 2 {
				t.Errorf("expected exit code 2, got %d", exitCode)
			}

			if output.Len() != 0 {
				t.Errorf(
					"expected no output, got %q",
					output.String(),
				)
			}
		})
	}
}
func TestRunSuccessfulResponsesInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/first" {
				time.Sleep(50 * time.Millisecond)
			}
			w.WriteHeader(http.StatusOK)
		},
	))
	defer server.Close()

	firstURL := server.URL + "/first"
	secondURL := server.URL + "/second"

	var output bytes.Buffer

	exitCode, err := run(
		context.Background(),
		[]string{
			"-workers", "2",
			"-timeout", "1s",
			firstURL,
			secondURL,
		},
		server.Client(),
		&output,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	got := output.String()

	firstPosition := strings.Index(got, firstURL)
	secondPosition := strings.Index(got, secondURL)

	if firstPosition == -1 || secondPosition == -1 {
		t.Fatalf("expected both URLs in output, got %q", got)
	}

	if firstPosition >= secondPosition {
		t.Errorf("results are out of order: %q", got)
	}

	expectedSummary := "Checked: 2, healthy: 2, failed: 0\n"
	if !strings.Contains(got, expectedSummary) {
		t.Errorf(
			"expected summary %q, got %q",
			expectedSummary,
			got,
		)
	}
}
func TestRunHTTP500IsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	))
	defer server.Close()

	var output bytes.Buffer

	exitCode, err := run(
		context.Background(),
		[]string{server.URL},
		server.Client(),
		&output,
	)

	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	got := output.String()

	if !strings.Contains(got, "Checked "+server.URL+": 500") {
		t.Errorf("expected HTTP 500 result, got %q", got)
	}

	expectedSummary := "Checked: 1, healthy: 0, failed: 1\n"
	if !strings.Contains(got, expectedSummary) {
		t.Errorf(
			"expected summary %q, got %q",
			expectedSummary,
			got,
		)
	}
}
func TestRunTransportErrorIsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	))

	client := server.Client()
	url := server.URL
	server.Close()

	var output bytes.Buffer

	exitCode, err := run(
		context.Background(),
		[]string{"-timeout", "1s", url},
		client,
		&output,
	)

	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	got := output.String()

	if !strings.Contains(got, "Error checking "+url+":") {
		t.Errorf("expected transport error in output, got %q", got)
	}

	expectedSummary := "Checked: 1, healthy: 0, failed: 1\n"
	if !strings.Contains(got, expectedSummary) {
		t.Errorf(
			"expected summary %q, got %q",
			expectedSummary,
			got,
		)
	}
}
func TestRunRejectsInvalidTimeoutValue(t *testing.T) {
	var output bytes.Buffer

	exitCode, err := run(
		context.Background(),
		[]string{
			"-timeout",
			"not-a-duration",
			"http://example.invalid",
		},
		http.DefaultClient,
		&output,
	)

	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("expected understandable parse error, got %v", err)
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout flag in error, got %v", err)
	}

	if exitCode != 2 {
		t.Errorf("expected exit code 2, got %d", exitCode)
	}

	if output.Len() != 0 {
		t.Errorf("expected no output, got %q", output.String())
	}
}
func TestRunAppliesClientTimeout(t *testing.T) {
	requestCanceled := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				requestCanceled <- struct{}{}
			case <-time.After(time.Second):
				w.WriteHeader(http.StatusOK)
			}
		},
	))
	defer server.Close()

	var output bytes.Buffer

	exitCode, err := run(
		context.Background(),
		[]string{"-timeout", "20ms", server.URL},
		server.Client(),
		&output,
	)

	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	select {
	case <-requestCanceled:
		// Client timeout отменил context HTTP-запроса.
	case <-time.After(time.Second):
		t.Fatal("HTTP request context was not canceled")
	}

	got := output.String()

	if !strings.Contains(got, "Error checking "+server.URL+":") {
		t.Errorf("expected timeout error in output, got %q", got)
	}

	expectedSummary := "Checked: 1, healthy: 0, failed: 1\n"
	if !strings.Contains(got, expectedSummary) {
		t.Errorf(
			"expected summary %q, got %q",
			expectedSummary,
			got,
		)
	}
}
func TestRunContextCancellationIsFailure(t *testing.T) {
	started := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		},
	))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-started
		cancel()
	}()

	var output bytes.Buffer

	exitCode, err := run(
		ctx,
		[]string{"-timeout", "1s", server.URL},
		server.Client(),
		&output,
	)

	if err != nil {
		t.Fatalf("unexpected configuration error: %v", err)
	}

	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	got := output.String()

	if !strings.Contains(got, "Error checking "+server.URL+":") {
		t.Errorf("expected cancellation error in output, got %q", got)
	}

	expectedSummary := "Checked: 1, healthy: 0, failed: 1\n"
	if !strings.Contains(got, expectedSummary) {
		t.Errorf(
			"expected summary %q, got %q",
			expectedSummary,
			got,
		)
	}
}
func TestIsHealthy(t *testing.T) {
	tests := []struct {
		name   string
		result CheckResult
		want   bool
	}{
		{
			name:   "status 200",
			result: CheckResult{StatusCode: 200},
			want:   true,
		},
		{
			name:   "status 399",
			result: CheckResult{StatusCode: 399},
			want:   true,
		},
		{
			name:   "status 400",
			result: CheckResult{StatusCode: 400},
			want:   false,
		},
		{
			name:   "status 599",
			result: CheckResult{StatusCode: 599},
			want:   false,
		},
		{
			name: "transport error",
			result: CheckResult{
				Err: errors.New("transport failed"),
			},
			want: false,
		},
		{
			name: "context canceled",
			result: CheckResult{
				Err: context.Canceled,
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isHealthy(test.result)
			if got != test.want {
				t.Errorf(
					"isHealthy(%+v) = %t, want %t",
					test.result,
					got,
					test.want,
				)
			}
		})
	}
}
