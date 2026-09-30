package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type CachedResponse struct {
	StatusCode int
	Header     map[string][]string
	Body       []byte
}

func getCacheKey(method string, requestURI string) string {
	rawKey := method + " " + requestURI
	hash := sha256.Sum256([]byte(rawKey))

	return fmt.Sprintf("%x.json", hash)
}

func main() {
	var port int
	var origin string
	var clearCache bool

	flag.IntVar(&port, "port", 0, "Port on which the caching proxy server will run")
	flag.StringVar(&origin, "origin", "", "URL of the server to which requests will be forwarded")
	flag.BoolVar(&clearCache, "clear-cache", false, "Clear the cache")

	flag.Parse()

	cacheDir := ".cache"

	if clearCache {
		fmt.Println("Clearing cache...")

		if err := os.RemoveAll(cacheDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error clearing cache: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Cache cleared successfully")
		return
	}
	os.MkdirAll(cacheDir, 0755)

	if port <= 0 || port > 65535 || origin == "" {
		fmt.Fprintln(os.Stderr, "Error: Both --port (1-65535) and --origin are required to start the proxy.")
		flag.Usage()
		os.Exit(1)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cacheFileName := getCacheKey(r.Method, r.URL.RequestURI())
		cacheFilePath := filepath.Join(cacheDir, cacheFileName)

		cachedBytes, err := os.ReadFile(cacheFilePath)
		// cache HIT
		if err == nil {
			var cached CachedResponse

			var unmarshalErr error = json.Unmarshal(cachedBytes, &cached)

			if unmarshalErr == nil {
				for headerKey, headerValues := range cached.Header {
					for _, headerValue := range headerValues {
						w.Header().Add(headerKey, headerValue)
					}
				}

				w.Header().Set("X-Cache", "HIT")

				w.WriteHeader(cached.StatusCode)
				w.Write(cached.Body)

				return
			}
		}

		// cache MISS
		targetUrl := strings.TrimRight(origin, "/") + r.URL.RequestURI()
		outboundReq, reqErr := http.NewRequest(r.Method, targetUrl, nil)
		if reqErr != nil {
			http.Error(w, "Failed to create outbound request", http.StatusInternalServerError)
			return
		}

		res, clientErr := http.DefaultClient.Do(outboundReq)
		if clientErr != nil {
			http.Error(w, "Error forwarding request to origin", http.StatusBadGateway)
			return
		}

		defer res.Body.Close()
		bodyBytes, readErr := io.ReadAll(res.Body)
		if readErr != nil {
			http.Error(w, "Failed to read response body from origin", http.StatusInternalServerError)
			return
		}

		resToCache := CachedResponse{
			StatusCode: res.StatusCode,
			Body:       bodyBytes,
			Header:     res.Header,
		}

		serialized, err := json.Marshal(resToCache)
		if err == nil {
			os.WriteFile(cacheFilePath, serialized, 0644)
		}

		for headerKey, headerValues := range res.Header {
			for _, headerValue := range headerValues {
				w.Header().Add(headerKey, headerValue)
			}
		}

		w.Header().Set("X-Cache", "MISS")
		w.WriteHeader(res.StatusCode)
		w.Write(bodyBytes)
	})

	var serverAddress string = fmt.Sprintf(":%d", port)
	fmt.Printf("Starting caching proxy on port %d, forwarding to %s\n", port, origin)

	serverErr := http.ListenAndServe(serverAddress, nil)
	if serverErr != nil {
		fmt.Fprintf(os.Stderr, "Server failed to start: %v\n", serverErr)
		os.Exit(1)
	}
}
