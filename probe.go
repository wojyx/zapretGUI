package main

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type probeTarget struct {
	Name string
	URL  string
}

// Subset of utils/targets.txt of the original repository: the endpoints
// that actually get blocked or throttled for Discord and YouTube.
var probeTargets = []probeTarget{
	{"discord.com", "https://discord.com"},
	{"gateway.discord.gg", "https://gateway.discord.gg"},
	{"cdn.discordapp.com", "https://cdn.discordapp.com"},
	{"www.youtube.com", "https://www.youtube.com"},
	{"i.ytimg.com", "https://i.ytimg.com"},
	{"googlevideo.com", "https://redirector.googlevideo.com"},
}

type probeResult struct {
	OK     int
	Total  int
	Failed []string
}

func (r probeResult) AllOK() bool { return r.Total > 0 && r.OK == r.Total }

// probe fetches every target in parallel. Any HTTP answer counts as success:
// we only care that the TLS connection can be established.
func probe(ctx context.Context, targets []probeTarget) probeResult {
	res := probeResult{Total: len(targets)}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t probeTarget) {
			defer wg.Done()
			err := probeOne(ctx, t.URL)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				res.OK++
			} else {
				res.Failed = append(res.Failed, t.Name)
			}
		}(t)
	}
	wg.Wait()
	return res
}

func probeOne(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	tr := &http.Transport{
		Proxy:               nil, // never go through a system proxy: we test the direct path
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.Body.Close()
}
