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
// default to 250 ms and 200 ms (ADR 0023). Discover runs in the background,
// at most once per Interval, so a slow lookup never holds up a round.
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
	// retainFor is how long a worker stays in frames after it was last seen
	// in DNS or answering, so the page can show it as unreachable. Docker
	// removes a killed container's name at once.
	retainFor = 60 * time.Second
	// maxStatus and maxMetrics bound what one source may send. An answer over
	// its cap is a gap, so a runaway source cannot use up the showcase's
	// memory limit. A big job's status can run past 256 KiB, so the status
	// cap is 1 MiB.
	maxStatus  = 1 << 20
	maxMetrics = 256 << 10
)

// entry is one worker the sampler tracks. A live entry is keyed by its
// address and is scraped each round. A retained entry is keyed by its
// address and last host, and is not scraped: its address now answers as
// another worker, so the old one stays listed as unreachable until retainFor
// has passed since it last answered.
type entry struct {
	addr     string
	host     string    // the host it last answered with, "" if none
	answered time.Time // when it last answered
	lastSeen time.Time // when it was last in DNS or answered; for a retained entry, when it last answered
	retained bool
}

// Sample polls the coordinator and every worker found, once per round, and
// calls out with each round's frame. A round that overruns Interval delays
// the next one; rounds are never queued. Sample returns nil when ctx ends,
// and an error only if the Config is unusable. The discovery goroutine ends
// when ctx ends, as soon as its lookup returns.
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

	var mu sync.Mutex
	var discovered []string // the set the latest completed lookup returned
	if c.Discover != nil {
		go watch(ctx, c.Discover, interval, func(addrs []string) {
			mu.Lock()
			discovered = addrs
			mu.Unlock()
		})
	}

	entries := make(map[string]*entry)
	var prev time.Time
	for seq := uint64(1); ctx.Err() == nil; seq++ {
		start := now()
		mu.Lock()
		found := discovered
		mu.Unlock()
		for _, a := range found {
			e := entries[a]
			if e == nil {
				e = &entry{addr: a}
				entries[a] = e
			}
			e.lastSeen = start
		}
		for k, e := range entries {
			if start.Sub(e.lastSeen) >= retainFor {
				delete(entries, k)
			}
		}

		var liveKeys []string
		for k, e := range entries {
			if !e.retained {
				liveKeys = append(liveKeys, k)
			}
		}
		slices.Sort(liveKeys)
		results := make([]WorkerSample, len(liveKeys))

		frame := Frame{Seq: seq, At: start}
		if seq > 1 {
			frame.SampleInterval = start.Sub(prev)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			frame.Status, frame.StatusOK = status(ctx, client, timeout, c.StatusURL)
		}()
		for i, k := range liveKeys {
			addr := entries[k].addr
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = scrape(ctx, client, timeout, addr)
			}()
		}
		wg.Wait()
		if ctx.Err() != nil {
			return nil // the round was cut short by the stop, not by a source
		}

		for i, k := range liveKeys {
			e := entries[k]
			ws := results[i]
			if !ws.OK {
				ws.Host = e.host // a gap still says which worker it is
				results[i] = ws
				continue
			}
			if ws.Host == "" {
				ws.Host = e.host
			} else {
				// The address answers as ws.Host now, so a retained entry
				// for that host is out of date: that worker is live again.
				delete(entries, e.addr+"\x00"+ws.Host)
				if e.host != "" && ws.Host != e.host {
					// The address now answers as another worker. The old
					// one stays listed, unreachable, for the rest of its
					// retainFor.
					if start.Sub(e.answered) < retainFor {
						entries[e.addr+"\x00"+e.host] = &entry{addr: e.addr, host: e.host, answered: e.answered, lastSeen: e.answered, retained: true}
					}
				}
			}
			e.host, e.answered, e.lastSeen = ws.Host, start, start
			results[i] = ws
		}

		res := make(map[string]WorkerSample, len(liveKeys))
		for i, k := range liveKeys {
			res[k] = results[i]
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		frame.Workers = make([]WorkerSample, 0, len(keys))
		for _, k := range keys {
			if e := entries[k]; e.retained {
				frame.Workers = append(frame.Workers, WorkerSample{Address: e.addr, Host: e.host})
			} else {
				frame.Workers = append(frame.Workers, res[k])
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

// watch looks workers up with discover, then waits interval, until ctx
// ends. A failed lookup keeps the set the last good one returned, and is
// logged once, unless ctx has already ended.
func watch(ctx context.Context, discover func(context.Context) ([]string, error), interval time.Duration, set func([]string)) {
	failing := false
	for {
		addrs, err := discover(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if !failing {
				slog.Warn("showcase: discovery failed; keeping the last workers it found", "err", err)
			}
			failing = true
		} else {
			failing = false
			set(addrs)
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// status returns the coordinator's /status.json as it came, and whether it
// answered with valid JSON. A garbage answer is a gap, like no answer.
func status(ctx context.Context, client *http.Client, timeout time.Duration, url string) (json.RawMessage, bool) {
	body, ok := get(ctx, client, timeout, url, maxStatus)
	if !ok || !json.Valid(body) {
		return nil, false
	}
	return body, true
}

// scrape reads one worker's /metrics. An answer with no byte counters is a
// gap, as is one that did not answer.
func scrape(ctx context.Context, client *http.Client, timeout time.Duration, addr string) WorkerSample {
	ws := WorkerSample{Address: addr}
	body, ok := get(ctx, client, timeout, "http://"+addr+"/metrics", maxMetrics)
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

// get reads url with a 200 answer within timeout, and at most limit bytes.
func get(ctx context.Context, client *http.Client, timeout time.Duration, url string, limit int64) ([]byte, bool) {
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
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
