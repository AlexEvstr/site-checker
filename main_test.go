package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	results := checkURLs(context.Background(), server.Client(), urls, 2)

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
	results := checkURLs(context.Background(), client, urls, 2)

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
	var exceeded atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)

		if current > workers {
			exceeded.Store(true)
		}

		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urls := make([]string, 12)

	for i := range urls {
		urls[i] = fmt.Sprintf("%s/%d", server.URL, i)
	}

	results := checkURLs(
		context.Background(),
		server.Client(),
		urls,
		workers,
	)
	if exceeded.Load() {
		t.Fatalf("number of concurrent requests exceeded %d", workers)
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
	done := make(chan []CheckResult, 1)

	go func() {
		done <- checkURLs(
			ctx,
			server.Client(),
			[]string{server.URL},
			1,
		)
	}()
	select {
	case <-started:
		// Запрос дошёл до handler.
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case results := <-done:
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
