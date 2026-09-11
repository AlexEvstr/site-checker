package main

import (
	"net/http"
	"net/http/httptest"
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

	result := checkURL(client, server.URL)

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

	result := checkURL(client, url)

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

	result := checkURL(client, server.URL)

	if result.Err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if result.StatusCode != 0 {
		t.Errorf("expected status code 0, got %d", result.StatusCode)
	}
}
