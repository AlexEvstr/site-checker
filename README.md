# Site Checker

Site Checker is a command-line utility that checks multiple HTTP URLs concurrently while preserving their original order in the output.

## Requirements

- Go 1.26.2 or newer

## Running

Pass one or more URLs after the options:

```bash
go run . -workers 3 -timeout 5s https://go.dev https://example.com
```

At least one URL is required.

## Options

- `-workers` sets the maximum number of concurrent URL checks. The value must be positive. The default is `3`.
- `-timeout` sets the timeout for each HTTP request. It accepts Go duration values such as `500ms`, `5s`, or `1m`. The value must be positive. The default is `5s`.

## Results and exit codes

HTTP statuses from `200` through `399` are considered healthy. HTTP statuses from `400` through `599`, transport errors, request timeouts, and context cancellation are considered failed checks.

After printing every URL result, Site Checker prints a summary:

```text
Checked: 3, healthy: 2, failed: 1
```

The process uses these exit codes:

- `0`: every URL is healthy;
- `1`: at least one URL check failed;
- `2`: the command-line configuration is invalid, for example because no URL was supplied or `-workers` or `-timeout` is invalid.

Failures for individual URLs are reported in the normal results and are not returned as command-line configuration errors.

## Example output

Successful checks:

```text
Checked https://go.dev: 200 in 180ms
Checked https://example.com: 200 in 95ms
Checked: 2, healthy: 2, failed: 0
```

A failed HTTP check:

```text
Checked https://example.com/unavailable: 500 in 42ms
Checked: 1, healthy: 0, failed: 1
```

A transport error:

```text
Error checking http://127.0.0.1: connection refused in 1ms
Checked: 1, healthy: 0, failed: 1
```

Durations and transport error details depend on the environment.

## Worker pool

Site Checker uses a fixed-size worker pool. URLs are sent to workers through a jobs channel, and workers send completed checks through a results channel. At most the configured number of workers can perform requests at the same time.

Each job and result carries the URL's original index. This lets requests finish in any order while the final output remains in the same order as the command-line arguments.

## Client timeout and context cancellation

The `-timeout` option configures `http.Client.Timeout`. It limits the total time allowed for each individual HTTP request.

The context passed to the checker controls the complete run. The CLI connects it to `Ctrl+C`, so cancellation is propagated to all in-flight requests. Client timeout and context cancellation are independent: one limits a single request, while the other can stop the whole operation.

## Tests

Run the ordinary test suite:

```bash
go test ./...
```

Run static analysis:

```bash
go vet ./...
```

Run the tests with the race detector:

```bash
go test -race -count=1 ./...
```
