package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"sync"
	"time"
)

func main() {
	target := flag.String("target", "http://localhost", "base URL")
	hackathonID := flag.String("hackathon", "", "hackathon id")
	viewers := flag.Int("viewers", 200, "concurrent viewers")
	duration := flag.Duration("duration", 30*time.Second, "load duration")
	scoring := flag.Bool("scoring", false, "measure scoring run duration instead of HTTP load")
	email := flag.String("email", "organizer@demo.io", "organizer email")
	password := flag.String("password", "demo1234", "organizer password")
	flag.Parse()

	if *hackathonID == "" {
		fmt.Fprintln(os.Stderr, "-hackathon is required")
		os.Exit(1)
	}

	if *scoring {
		runScoringBenchmark(*target, *hackathonID, *email, *password)
		return
	}
	runHTTPLoad(*target, *hackathonID, *viewers, *duration)
}

func runHTTPLoad(target, hackathonID string, viewers int, duration time.Duration) {
	url := target + "/api/hackathons/" + hackathonID + "/leaderboard"
	deadline := time.Now().Add(duration)
	var mu sync.Mutex
	latencies := []time.Duration{}
	errors := 0
	statuses := map[int]int{}
	var wg sync.WaitGroup

	fmt.Printf("load test: %d viewers for %s against %s\n", viewers, duration, url)
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 10 * time.Second}
			for time.Now().Before(deadline) {
				start := time.Now()
				res, err := client.Get(url)
				elapsed := time.Since(start)
				mu.Lock()
				if err != nil {
					errors++
				} else {
					latencies = append(latencies, elapsed)
					statuses[res.StatusCode]++
					res.Body.Close()
				}
				mu.Unlock()
				time.Sleep(500 * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		idx := int(float64(len(latencies)-1) * p)
		return latencies[idx]
	}
	fmt.Printf("requests: %d, errors: %d, statuses: %v\n", len(latencies), errors, statuses)
	fmt.Printf("p50: %s, p95: %s, p99: %s, max: %s\n",
		pct(0.50), pct(0.95), pct(0.99), latencies[len(latencies)-1])
}

func runScoringBenchmark(target, hackathonID, email, password string) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 60 * time.Second}

	loginBody, _ := json.Marshal(map[string]string{"email": email, "password": password})
	res, err := client.Post(target+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil || res.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
		os.Exit(1)
	}
	var loginResp struct {
		CSRFToken string `json:"csrf_token"`
	}
	json.NewDecoder(res.Body).Decode(&loginResp)
	res.Body.Close()

	for i := 0; i < 3; i++ {
		start := time.Now()
		req, _ := http.NewRequest("POST", target+"/api/hackathons/"+hackathonID+"/scoring/recalculate", nil)
		req.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
		res, err := client.Do(req)
		if err != nil || res.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "recalculate failed: %v %v\n", err, res)
			os.Exit(1)
		}
		var out struct {
			TeamsScored int    `json:"teams_scored"`
			Status      string `json:"status"`
		}
		json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		fmt.Printf("scoring run %d: %s (%d teams, status %s)\n", i+1, time.Since(start).Round(time.Millisecond), out.TeamsScored, out.Status)
	}
}
