package main

import (
	"io/ioutil"
	"math"
	"os"
	"strconv"
	"strings"
)

// ---- Given: the HTTP plumbing. Read it, then leave it alone. ----

type Request struct {
	Now     float64           // simulated clock, seconds since the epoch
	Method  string            // "GET", "POST", ...
	Target  string            // request target as sent, query string included
	Headers map[string]string // lower-case name -> value, spaces trimmed
}

// header returns a request header's value (pass the name in lower case), or "" if it is absent.
func header(r *Request, name string) string {
	return r.Headers[name]
}

var _ = math.Floor // math is imported for your use; this keeps the import legal if you do not need it

var out strings.Builder

func reason(code int) string {
	switch code {
	case 200:
		return "OK"
	case 429:
		return "Too Many Requests"
	}
	return "Unknown"
}

func statusLine(code int) {
	out.WriteString("HTTP/1.1 " + strconv.Itoa(code) + " " + reason(code) + "\r\n")
}

func addHeader(name, value string) {
	out.WriteString(name + ": " + value + "\r\n")
}

func addHeaderInt(name string, value int64) {
	out.WriteString(name + ": " + strconv.FormatInt(value, 10) + "\r\n")
}

func endHead() {
	out.WriteString("Content-Length: 0\r\n\r\n")
}

const loginLimit = 3 // requests per window for POST /login
const apiLimit = 10  // requests per window for everything else
const window = 60    // window length in seconds

type windowCount struct {
	start int64 // start of the window this counter last saw
	count int64 // requests counted in that window
}

var counts = map[string]*windowCount{} // "client|class" -> its window

func handle(r *Request) {
	c := header(r, "x-api-key")
	if c == "" {
		c = "anonymous"
	}

	// TODO 1: the route class: login for POST with the path (target up to '?') exactly "/login", else api.
	path, _, _ := strings.Cut(r.Target, "?")
	class := "api"
	limit := int64(apiLimit)
	if r.Method == "POST" && path == "/login" {
		class = "login"
		limit = int64(loginLimit)
	}
	client := c + "|" + class

	// TODO 2: the window start, then this (client, class)'s window in counts (stale -> zero).
	start := int64(math.Floor(r.Now/window)) * window

	w, ok := counts[client]
	if !ok || w.start < start {
		w = &windowCount{start: start}
		counts[client] = w
	}

	// TODO 4: X-RateLimit-Limit, -Remaining (after counting), -Reset (window end, epoch seconds), in that order.
	if w.count >= limit {
		statusLine(429)
		addHeaderInt("Retry-After", int64(math.Ceil(float64(start+window)-r.Now)))
		addHeaderInt("X-RateLimit-Limit", limit)
		addHeaderInt("X-RateLimit-Remaining", limit-w.count)
		addHeaderInt("X-RateLimit-Reset", start+window)
		endHead()
		return
	}
	w.count++
	statusLine(200)
	addHeaderInt("X-RateLimit-Limit", limit)
	addHeaderInt("X-RateLimit-Remaining", limit-w.count)
	addHeaderInt("X-RateLimit-Reset", start+window)
	endHead()
}

func directive(line string, now float64) {}

func main() {
	raw, _ := ioutil.ReadAll(os.Stdin)
	data := string(raw)
	pos := 0
	now := 0.0
	for pos < len(data) {
		if data[pos] == '@' {
			eol := strings.IndexByte(data[pos:], '\n')
			if eol < 0 {
				eol = len(data)
			} else {
				eol += pos
			}
			line := strings.TrimSpace(data[pos:eol])
			if strings.HasPrefix(line, "@now ") {
				now, _ = strconv.ParseFloat(strings.TrimSpace(line[5:]), 64)
			} else {
				directive(line, now)
			}
			pos = eol + 1
			continue
		}
		rel := strings.Index(data[pos:], "\r\n\r\n")
		if rel < 0 {
			break // an unfinished request head gets no reply
		}
		end := pos + rel
		lines := strings.Split(data[pos:end], "\r\n")
		pos = end + 4
		words := strings.Split(lines[0], " ")
		req := &Request{Now: now, Method: words[0], Headers: map[string]string{}}
		if len(words) > 1 {
			req.Target = words[1]
		}
		for _, text := range lines[1:] {
			colon := strings.IndexByte(text, ':')
			if colon <= 0 {
				continue
			}
			name := strings.ToLower(text[:colon])
			value := strings.Trim(text[colon+1:], " \t")
			if old, ok := req.Headers[name]; ok {
				value = old + ", " + value
			}
			req.Headers[name] = value
		}
		handle(req)
	}
	os.Stdout.WriteString(out.String())
}
