package showcase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Config says what a sampler polls and how often. Interval and Timeout
// default to 250 ms and 200 ms (ADR 0023).
type Config struct {
	StatusURL string                                  // the coordinator's /status.json
	Discover  func(context.Context) ([]string, error) // worker addresses as host:port; nil means none
	Interval  time.Duration                           // between the starts of rounds
	Timeout   time.Duration                           // for each request in a round
	Now       func() time.Time                        // nil means time.Now
	Client    *http.Client                            // nil means http.DefaultClient
}

const (
	defaultInterval = 250 * time.Millisecond
	defaultTimeout  = 200 * time.Millisecond
	// retainFor is how long a worker that has left DNS stays in frames,
	// unreachable, so the page can show it: Docker removes a killed
	// container's name at once, and the page must still show it.
	retainFor = 60 * time.Second
	// maxBody bounds what one source may send, so that a runaway answer
	// cannot use up the showcase's memory limit.
	maxBody = 4 << 20
)

// Sample polls the coordinator and every worker found, once per round, and
// calls out with each round's frame. A round that overruns Interval delays
// the next one; rounds are never queued. Sample returns nil when ctx ends,
// and an error only if the Config is unusable.
func Sample(ctx context.Context, c Config, out func(Frame)) error {
	if c.StatusURL == "" {
		return errors.New("showcase: no status URL to sample")
	}
	interval, timeout := c.Interval, c.Timeout
	if interval <= 0 {
		interval = defaultInterval
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	now, client := c.Now, c.Client
	if now == nil {
		now = time.Now
	}
	if client == nil {
		client = http.DefaultClient
	}

	var discovered []string                // the addresses DNS last gave
	lastSeen := make(map[string]time.Time) // when each address was last in DNS or answered
	var prev time.Time
	discoverDown := false
	for seq := uint64(1); ctx.Err() == nil; seq++ {
		start := now()
		found, err := discover(ctx, c.Discover)
		if err != nil {
			if !discoverDown {
				slog.Warn("showcase: discovery failed; keeping the last workers it found", "err", err)
			}
			discoverDown = true
		} else {
			discovered = found
			discoverDown = false
		}
		for _, a := range discovered {
			lastSeen[a] = start
		}
		for a, t := range lastSeen {
			if start.Sub(t) >= retainFor {
				delete(lastSeen, a)
			}
		}
		addrs := make([]string, 0, len(lastSeen))
		for a := range lastSeen {
			addrs = append(addrs, a)
		}
		slices.Sort(addrs)

		frame := Frame{Seq: seq, At: start, Workers: make([]WorkerSample, len(addrs))}
		if seq > 1 {
			frame.SampleInterval = start.Sub(prev)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			frame.Status, frame.StatusOK = status(ctx, client, timeout, c.StatusURL)
		}()
		for i, a := range addrs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				frame.Workers[i] = scrape(ctx, client, timeout, a)
			}()
		}
		wg.Wait()
		if ctx.Err() != nil {
			return nil // the round was cut short by the stop, not by a source
		}
		for _, w := range frame.Workers {
			if w.OK {
				lastSeen[w.Address] = start
			}
		}
		out(frame)
		prev = start

		if wait := interval - now().Sub(start); wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
			case <-t.C:
			}
		}
	}
	return nil
}

// discover asks the Config's Discover for the worker addresses, or returns
// none if there is no Discover.
func discover(ctx context.Context, f func(context.Context) ([]string, error)) ([]string, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx)
}

// status returns the coordinator's /status.json as it came, and whether it
// answered with valid JSON. A garbage answer is a gap, like no answer.
func status(ctx context.Context, client *http.Client, timeout time.Duration, url string) (json.RawMessage, bool) {
	body, ok := get(ctx, client, timeout, url)
	if !ok || !json.Valid(body) {
		return nil, false
	}
	return body, true
}

// scrape reads one worker's /metrics. A worker whose answer has no byte
// counters is a gap, as is one that did not answer.
func scrape(ctx context.Context, client *http.Client, timeout time.Duration, addr string) WorkerSample {
	ws := WorkerSample{Address: addr}
	body, ok := get(ctx, client, timeout, "http://"+addr+"/metrics")
	if !ok {
		return ws
	}
	bytes, host := ParseMetrics(string(body))
	if bytes == nil {
		return ws
	}
	ws.Bytes, ws.Host, ws.OK = bytes, host, true
	return ws
}

// get reads url with a 200 answer within timeout, and at most maxBody bytes.
func get(ctx context.Context, client *http.Client, timeout time.Duration, url string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, false
	}
	return body, true
}

// DNSDiscover returns a Discover that looks name up in DNS and returns each
// address found, with port. In Compose, "worker" names every worker. A name
// with no records is no workers, not an error.
func DNSDiscover(name string, port int) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		ips, err := net.DefaultResolver.LookupHost(ctx, name)
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		addrs := make([]string, 0, len(ips))
		for _, ip := range ips {
			addrs = append(addrs, net.JoinHostPort(ip, strconv.Itoa(port)))
		}
		return addrs, nil
	}
}
